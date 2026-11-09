// Command latch-control is the S1 skeleton of the Latch SaaS control plane: it
// receives relay heartbeats (fleet inventory + usage) and drives relays' mTLS
// control APIs to mint tokens. Accounts, multi-tenancy and billing are S2/S3.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/app"
	"github.com/kayaramazan/latch/latch-control/internal/panel"
	"github.com/kayaramazan/latch/latch-control/internal/plane"
	"github.com/kayaramazan/latch/latch-control/internal/store"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "gen-certs":
		err = genCerts(os.Args[2:])
	case "version":
		fmt.Println("latch-control", Version)
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "latch-control:", err)
		os.Exit(1)
	}
}

func genCerts(args []string) error {
	fs := flag.NewFlagSet("gen-certs", flag.ContinueOnError)
	dir := fs.String("dir", "./control-certs", "output directory")
	hosts := fs.String("relay-hosts", "127.0.0.1,localhost", "comma-separated relay control hosts/IPs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var list []string
	for _, h := range splitComma(*hosts) {
		list = append(list, h)
	}
	if err := plane.GenCerts(*dir, list); err != nil {
		return err
	}
	fmt.Printf("wrote CA + relay-control + control-plane certs to %s\n", *dir)
	fmt.Println("relay:   --control-cert relay-control.crt --control-key relay-control.key --control-client-ca ca.crt")
	fmt.Println("control: --client-cert control-plane.crt --client-key control-plane.key --relay-ca ca.crt")
	return nil
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func usage() {
	fmt.Fprint(os.Stderr, `latch-control — Latch SaaS control plane (S1 skeleton)

Usage:
  latch-control serve --listen 127.0.0.1:9000 --reg-token TOKEN \
     --client-cert cp.crt --client-key cp.key --relay-ca relay-ca.crt

Endpoints: POST /relays/heartbeat (relays), GET /relays, POST /relays/{id}/tokens.
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9000", "control-plane listen address (keep private)")
	regToken := fs.String("reg-token", "", "bearer token relays present on heartbeat")
	clientCert := fs.String("client-cert", "", "PEM client certificate to call relay control APIs")
	clientKey := fs.String("client-key", "", "PEM client key")
	relayCA := fs.String("relay-ca", "", "PEM CA that signs relay control server certificates")
	opsToken := fs.String("ops-token", "", "bearer token for /ops endpoints")
	region := fs.String("region", "eu", "default region for new accounts")
	webhookSecret := fs.String("webhook-secret", "", "HMAC secret for MoR billing webhooks")
	checkoutURL := fs.String("checkout-url", "https://checkout.example.com", "MoR hosted-checkout base URL")
	dbDSN := fs.String("db", "", "PostgreSQL DSN (empty = in-memory, dev only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *clientCert == "" || *clientKey == "" || *relayCA == "" {
		return errors.New("--client-cert, --client-key and --relay-ca are required")
	}
	cert, err := tls.LoadX509KeyPair(*clientCert, *clientKey)
	if err != nil {
		return fmt.Errorf("client cert: %w", err)
	}
	caPEM, err := os.ReadFile(*relayCA)
	if err != nil {
		return fmt.Errorf("relay ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("relay ca is not a valid certificate")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := plane.New(plane.Options{RegToken: *regToken, ClientCert: cert, RelayCAs: pool, Logger: log})
	var st store.Store = store.NewMemory()
	if *dbDSN != "" {
		pg, err := store.OpenPostgres(context.Background(), *dbDSN)
		if err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		defer pg.Close()
		st = pg
		log.Info("using PostgreSQL store")
	} else {
		log.Warn("using in-memory store (data is lost on restart); set --db for production")
	}
	appSrv := app.New(app.Options{Store: st, Fleet: p, OpsToken: *opsToken, Region: *region, Issuer: "Latch",
		WebhookSecret: *webhookSecret, CheckoutBaseURL: *checkoutURL, Logger: log})
	p.SetMeter(appSrv)

	top := http.NewServeMux()
	top.Handle("/relays/", p.Handler())
	top.Handle("/api/", appSrv.Handler())
	top.Handle("/ops/", appSrv.Handler())
	top.Handle("/webhooks/", appSrv.Handler())
	top.Handle("/", panel.Handler())

	srv := &http.Server{Addr: *listen, Handler: top, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go appSrv.StartBackground(ctx)
	defer stop()
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	log.Info("latch-control listening", "addr", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
