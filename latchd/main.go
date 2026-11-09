// Command latchd is the Latch agent: a tunnel client (and, from phase 6, a
// local reverse proxy with embedded Caddy) managed through a local REST API
// and web UI. Subcommands other than `run` and `version` talk to a running
// daemon over its management API.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/api"
	"github.com/kayaramazan/latch/latchd/internal/cli"
	"github.com/kayaramazan/latch/latchd/internal/config"
	"github.com/kayaramazan/latch/latchd/internal/events"
	"github.com/kayaramazan/latch/latchd/internal/local"
	agentmetrics "github.com/kayaramazan/latch/latchd/internal/metrics"
	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/latchd/internal/tunnel"
	"github.com/kayaramazan/latch/proto/model"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "relay":
		err = cli.Relay(args)
	case "route":
		err = cli.Route(args)
	case "status":
		err = cli.Status(args)
	case "events":
		err = cli.Events(args)
	case "explain":
		err = cli.Explain(args)
	case "version", "-v", "--version":
		fmt.Println("latchd", Version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "latchd: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "latchd:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `latchd — Latch agent

Usage:
  latchd run [--data-dir DIR] [--listen HOST:PORT]
  latchd relay add --url latch://host:port/?token=... [--name NAME] [--ca-file FILE]
  latchd relay list
  latchd relay rm <relay-id|name>
  latchd route add --target HOST:PORT --tunnel <relay> --hostname NAME [--name NAME] [--path /]
  latchd route list
  latchd route rm <route-id>
  latchd status
  latchd events [--follow] [--route ID] [-n N]
  latchd explain --route ID --ip IP [--path /x]
  latchd version

Subcommands other than run talk to a running daemon (default http://127.0.0.1:4040,
override with --api or LATCHD_API).
`)
}

func run(args []string) error {
	cfg, err := cli.ParseRun(args)
	if err != nil {
		return err
	}
	logger := cli.NewLogger(cfg)

	paths, err := config.Resolve(cfg.DataDir)
	if err != nil {
		return err
	}
	logger.Info("latchd starting", "version", Version, "dataDir", paths.DataDir)

	st, err := store.Open(paths.DBPath, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()

	agentID, err := config.AgentID(paths)
	if err != nil {
		return err
	}

	snap0, _ := st.Current()
	evStore, err := events.Open(filepath.Join(paths.LogDir, "events.db"),
		events.Options{RetentionDays: snap0.Settings.Events.RetentionDays, MaxSizeMB: snap0.Settings.Events.MaxSizeMB}, logger)
	if err != nil {
		return err
	}
	defer evStore.Close()
	metrics := agentmetrics.New()
	sink := eventSink{metrics, evStore}

	listen := cfg.Listen
	if listen == "" {
		snap, _ := st.Current()
		listen = snap.Settings.Listen
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mgr := tunnel.NewManager(tunnel.Deps{Store: st, AgentID: agentID, AgentVersion: Version, Events: sink, Logger: logger})
	mgrDone := make(chan struct{})
	go func() {
		defer close(mgrDone)
		mgr.Run(ctx)
	}()

	// Local reverse-proxy mode (embedded Caddy). It only starts Caddy once a
	// local route exists, so tunnel-only installs bind nothing extra.
	snap, _ := st.Current()
	localMgr := local.NewManager(local.Deps{
		Store: st,
		Emit:  sink.Append,
		Options: local.Options{
			HTTPPort:   snap.Settings.LocalMode.HTTPPort,
			HTTPSPort:  snap.Settings.LocalMode.HTTPSPort,
			Issuer:     local.IssuerACME,
			HTTP3:      snap.Settings.LocalMode.HTTP3,
			StorageDir: paths.CaddyDir,
			ACMECA:     acmeDirectoryFor(snap.Settings.LocalMode.ACMECA),
		},
		Trusted: snap.Settings.TrustedProxies,
		Logger:  logger,
	})
	localDone := make(chan struct{})
	go func() {
		defer close(localDone)
		localMgr.Run(ctx)
	}()

	srv := api.New(st, mgr, evStore, Version, logger)
	srv.SetHealth(localMgr.Health)
	srv.SetMetrics(metrics.Handler())
	go updateAgentGauges(ctx, metrics, mgr, localMgr)
	err = srv.ListenAndServe(ctx, listen)

	<-mgrDone
	<-localDone
	if err != nil {
		return err
	}
	logger.Info("latchd stopped")
	return nil
}

// acmeDirectoryFor maps a settings ACME CA choice to a directory URL override
// ("" => Caddy's default Let's Encrypt/ZeroSSL).
func acmeDirectoryFor(ca string) string {
	switch ca {
	case "letsencrypt-staging":
		return "https://acme-staging-v02.api.letsencrypt.org/directory"
	default:
		return ""
	}
}

// eventSink fans each AccessEvent out to every sink (metrics + durable store).
type eventSink []interface {
	Append(ev model.AccessEvent)
}

func (s eventSink) Append(ev model.AccessEvent) {
	for _, x := range s {
		x.Append(ev)
	}
}

// updateAgentGauges refreshes Prometheus gauges from live state.
func updateAgentGauges(ctx context.Context, m *agentmetrics.Metrics, mgr *tunnel.Manager, lm *local.Manager) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			connected := 0
			for _, st := range mgr.Status() {
				if st.State == tunnel.StateConnected {
					connected++
				}
			}
			m.SetTunnels(connected)
			m.SetHealth(lm.Health())
		}
	}
}
