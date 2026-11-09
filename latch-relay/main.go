// Command latch-relay is the self-hosted Latch relay: it terminates TLS for
// public hostnames and forwards traffic to connected agents over tunnels.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/acme"
	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	relaycfg "github.com/kayaramazan/latch/latch-relay/internal/config"
	"github.com/kayaramazan/latch/latch-relay/internal/devca"
	"github.com/kayaramazan/latch/latch-relay/internal/relay"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

const usage = `latch-relay — self-hosted tunnel relay

Usage:
  latch-relay install [flags]        interactive setup wizard (config, systemd, first token)
  latch-relay run [flags]            start the relay
  latch-relay status [flags]         show config, listeners and registered domains
  latch-relay doctor [flags]         check DNS, ports, certificate and clock
  latch-relay token create [flags]   issue an agent token (prints a latch:// connection string)
  latch-relay token list
  latch-relay token revoke <id>
  latch-relay domain add <name> [flags]  register a custom domain for on-demand TLS
  latch-relay domain list
  latch-relay domain rm <name>
  latch-relay domain verify <name>
  latch-relay dev-ca init [flags]    create a development CA and server certificate
  latch-relay version

Run "latch-relay <command> -h" for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = cmdInstall(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "token":
		err = cmdToken(os.Args[2:])
	case "domain":
		err = cmdDomain(os.Args[2:])
	case "dev-ca":
		err = cmdDevCA(os.Args[2:])
	case "version":
		fmt.Println("latch-relay", Version, "protocol", tunnel.ProtocolVersion)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	if v := os.Getenv("LATCH_RELAY_DATA_DIR"); v != "" {
		return v
	}
	if os.Geteuid() == 0 {
		return "/var/lib/latch-relay"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "latch-relay")
}

func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		h = slog.NewTextHandler(os.Stderr, opts)
	default:
		return nil, fmt.Errorf("invalid log format %q", format)
	}
	return slog.New(h), nil
}

func parsePortRange(s string) ([2]int, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return [2]int{}, fmt.Errorf("tcp port range must look like 20000-20999")
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil {
		return [2]int{}, fmt.Errorf("tcp port range must look like 20000-20999")
	}
	return [2]int{lo, hi}, nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to a config file (e.g. "+relaycfg.DefaultPath+"); flags override it")
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory (also $LATCH_RELAY_DATA_DIR)")
	publicHost := fs.String("public-host", "", "this relay's hostname, e.g. relay.example.com (required)")
	baseDomain := fs.String("base-domain", "", "wildcard zone for generated subdomains (default: public-host)")
	publicIP := fs.String("public-ip", "", "this relay's public IP (used to verify custom-domain A records)")
	httpsAddr := fs.String("https-addr", ":443", "public HTTPS listen address")
	httpAddr := fs.String("http-addr", ":80", "HTTP redirect listen address (empty to disable)")
	agentAddr := fs.String("agent-addr", ":7443", "agent tunnel listen address")
	tlsCert := fs.String("tls-cert", "", "PEM certificate for the public and agent listeners")
	tlsKey := fs.String("tls-key", "", "PEM private key")
	dev := fs.Bool("dev", false, "use a development CA from <data-dir>/devca (created if missing)")
	acmeOn := fs.Bool("acme", false, "obtain certificates automatically via Let's Encrypt")
	acmeEmail := fs.String("acme-email", "", "contact email for Let's Encrypt")
	acmeDNS := fs.String("acme-dns", "manual", "DNS-01 provider for the wildcard: cloudflare|manual")
	cfToken := fs.String("cloudflare-token", "", "Cloudflare API token (or $"+relaycfg.CloudflareTokenEnv+")")
	acmeStaging := fs.Bool("acme-staging", false, "use the Let's Encrypt staging environment")
	tcpPorts := fs.String("tcp-ports", "20000-20999", "port pool for TCP tunnels")
	tcpBind := fs.String("tcp-bind", "", "bind host for TCP tunnel ports (default: all interfaces)")
	logLevel := fs.String("log-level", "info", "debug|info|warn|error")
	logFormat := fs.String("log-format", "text", "text|json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// A config file supplies defaults; explicit flags win.
	var cfg *relaycfg.Config
	if *configPath != "" {
		c, err := relaycfg.Load(*configPath)
		if err != nil {
			return err
		}
		cfg = c
		apply := func(name string, dst *string, val string) {
			if !set[name] && val != "" {
				*dst = val
			}
		}
		apply("data-dir", dataDir, cfg.DataDir)
		apply("public-host", publicHost, cfg.PublicHost)
		apply("base-domain", baseDomain, cfg.BaseDomain)
		apply("public-ip", publicIP, cfg.PublicIP)
		apply("https-addr", httpsAddr, cfg.HTTPSAddr)
		apply("http-addr", httpAddr, cfg.HTTPAddr)
		apply("agent-addr", agentAddr, cfg.AgentAddr)
		apply("tcp-ports", tcpPorts, cfg.TCPPorts)
		apply("tcp-bind", tcpBind, cfg.TCPBind)
		apply("log-level", logLevel, cfg.LogLevel)
		apply("log-format", logFormat, cfg.LogFormat)
		if cfg.ACME.Enabled && !set["acme"] {
			*acmeOn = true
		}
		apply("acme-email", acmeEmail, cfg.ACME.Email)
		apply("acme-dns", acmeDNS, cfg.ACME.DNSProvider)
		apply("cloudflare-token", cfToken, cfg.ACME.CloudflareToken)
		if cfg.ACME.Staging && !set["acme-staging"] {
			*acmeStaging = true
		}
	}

	if *publicHost == "" {
		return errors.New("--public-host is required (or set public_host in --config)")
	}
	log, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		return err
	}
	pr, err := parsePortRange(*tcpPorts)
	if err != nil {
		return err
	}
	if *baseDomain == "" {
		*baseDomain = *publicHost
	}

	r, err := relay.New(relay.Config{
		DataDir: *dataDir, PublicHost: *publicHost, BaseDomain: *baseDomain,
		HTTPSAddr: *httpsAddr, HTTPAddr: *httpAddr, AgentAddr: *agentAddr,
		TCPBindHost: *tcpBind, TCPPortRange: pr, Version: Version, Logger: log,
	})
	if err != nil {
		return err
	}

	var acmeMgr *acme.Manager
	switch {
	case *dev:
		dir := filepath.Join(*dataDir, "devca")
		files, err := devca.Init(dir, []string{*publicHost, "*." + *baseDomain, *baseDomain, "localhost", "127.0.0.1"}, 0)
		if err != nil {
			return fmt.Errorf("dev ca: %w", err)
		}
		cert, err := devca.LoadServerCert(files)
		if err != nil {
			return err
		}
		r.SetCertProvider(relay.StaticCert(cert), false)
		log.Warn("development certificates in use; agents must trust the CA", "ca", files.CACert)
	case *tlsCert != "" && *tlsKey != "":
		cert, err := loadKeyPair(*tlsCert, *tlsKey)
		if err != nil {
			return err
		}
		r.SetCertProvider(relay.StaticCert(cert), false)
	case *acmeOn:
		if *cfToken == "" {
			*cfToken = os.Getenv(relaycfg.CloudflareTokenEnv)
		}
		mgr, err := acme.New(acme.Options{
			DataDir: *dataDir, Email: *acmeEmail, BaseDomain: *baseDomain, PublicHost: *publicHost,
			PublicIP: *publicIP, Staging: *acmeStaging, DNSProvider: *acmeDNS, CloudflareToken: *cfToken,
			ManualHooks: acme.ManualHooks{Present: logManualRecord(log)},
			Domains:     r.Store(), Logger: log,
		})
		if err != nil {
			return err
		}
		acmeMgr = mgr
		r.SetCertProvider(mgr.GetCertificate, true)
	default:
		return errors.New("provide --acme (with --acme-email), --tls-cert and --tls-key, or --dev")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := r.Start(ctx); err != nil {
		return err
	}
	if acmeMgr != nil {
		defer acmeMgr.Stop()
		if err := acmeMgr.Start(ctx); err != nil {
			log.Error("could not start certificate management", "err", err)
		}
	}
	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.Stop(sctx)
}

// logManualRecord returns a manual-DNS Present hook for the daemon: it logs the
// required TXT record. (The install wizard uses an interactive hook instead.)
func logManualRecord(log *slog.Logger) func(context.Context, acme.ManualRecord) error {
	return func(ctx context.Context, rec acme.ManualRecord) error {
		log.Warn("manual DNS-01: create this TXT record, then the certificate will be issued on the next attempt",
			"name", rec.Name, "value", rec.Value)
		// Give the operator a window to add the record before this attempt fails.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Minute):
			return nil
		}
	}
}

func cmdToken(args []string) error {
	if len(args) == 0 {
		return errors.New("token: expected create, list or revoke")
	}
	switch args[0] {
	case "create":
		return cmdTokenCreate(args[1:])
	case "list":
		return cmdTokenList(args[1:])
	case "revoke":
		return cmdTokenRevoke(args[1:])
	default:
		return fmt.Errorf("token: unknown subcommand %q", args[0])
	}
}

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(dataDir, "relay.db"))
}

func cmdTokenCreate(args []string) error {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	name := fs.String("name", "", "token name, e.g. laptop (required)")
	hosts := fs.String("hostnames", "", "comma-separated allowed hostnames; *.zone allows one label under zone")
	tcp := fs.Bool("tcp", false, "allow TCP tunnels")
	expires := fs.Duration("expires", 0, "lifetime, e.g. 720h (0 = never)")
	host := fs.String("host", "", "relay hostname for the connection string (default: from `run` settings)")
	port := fs.Int("port", 0, "relay agent port for the connection string (default: from `run` settings)")
	dev := fs.Bool("dev", false, "embed the development CA in the connection string")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	var scope tunnel.Scope
	if *hosts != "" {
		scope.Hostnames = strings.Split(*hosts, ",")
	} else if bd, _ := st.Setting(ctx, store.SettingBaseDomain); bd != "" {
		scope.Hostnames = []string{"*." + bd}
	}
	scope.TCP = *tcp
	var exp *time.Time
	if *expires > 0 {
		t := time.Now().Add(*expires)
		exp = &t
	}
	plain, tok, err := auth.New(st).Create(ctx, *name, scope, exp)
	if err != nil {
		return err
	}

	if *host == "" {
		*host, _ = st.Setting(ctx, store.SettingPublicHost)
	}
	if *port == 0 {
		if p, _ := st.Setting(ctx, store.SettingAgentPort); p != "" {
			*port, _ = strconv.Atoi(p)
		}
	}
	ci := tunnel.ConnInfo{Host: *host, Port: *port, Token: plain, Name: *name}
	if *dev {
		pem, err := os.ReadFile(devca.Paths(filepath.Join(*dataDir, "devca")).CACert)
		if err != nil {
			return fmt.Errorf("--dev: %w (run `latch-relay run --dev` or `dev-ca init` first)", err)
		}
		ci.CAPEM = string(pem)
	}
	fmt.Printf("Token %s (%s) created. Scope: hostnames=%v tcp=%v\n", tok.ID, tok.Name, tok.Scope.Hostnames, tok.Scope.TCP)
	if *host == "" {
		fmt.Println("Relay host unknown (relay has not run yet); pass --host. Plain token:")
		fmt.Println(plain)
		return nil
	}
	fmt.Println("Paste this into the agent (shown once, not stored):")
	fmt.Println(ci.String())
	return nil
}

func cmdTokenList(args []string) error {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	toks, err := st.Tokens(context.Background())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tSCOPE\tCREATED\tLAST SEEN")
	for _, t := range toks {
		status := "active"
		switch {
		case t.RevokedAt != nil:
			status = "revoked"
		case t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt):
			status = "expired"
		}
		scope := strings.Join(t.Scope.Hostnames, ",")
		if t.Scope.TCP {
			scope += " +tcp"
		}
		seen := "never"
		if t.LastSeenAt != nil {
			seen = t.LastSeenAt.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, status, scope, t.CreatedAt.Local().Format(time.RFC3339), seen)
	}
	return w.Flush()
}

func cmdTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: latch-relay token revoke <id>")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := auth.New(st).Revoke(context.Background(), fs.Arg(0)); err != nil {
		return err
	}
	fmt.Println("Token revoked. A running relay disconnects its agents within 30 seconds.")
	return nil
}

func cmdDevCA(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: latch-relay dev-ca init [--dir DIR] [--hosts a,b]")
	}
	fs := flag.NewFlagSet("dev-ca init", flag.ContinueOnError)
	dir := fs.String("dir", filepath.Join(defaultDataDir(), "devca"), "directory for CA and server certificate")
	hosts := fs.String("hosts", "relay.local,*.relay.local,localhost,127.0.0.1", "comma-separated names for the server certificate")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	files, err := devca.Init(*dir, strings.Split(*hosts, ","), 0)
	if err != nil {
		return err
	}
	fmt.Printf("CA:          %s\nServer cert: %s\nServer key:  %s\n", files.CACert, files.Cert, files.Key)
	fmt.Println("Agents trust the CA with --ca-file, or embed it with `token create --dev`.")
	return nil
}

func loadKeyPair(certFile, keyFile string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(certFile, keyFile)
}
