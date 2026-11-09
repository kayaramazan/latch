package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	relaycfg "github.com/kayaramazan/latch/latch-relay/internal/config"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
)

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file to read settings from")
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var cfg *relaycfg.Config
	if *configPath != "" {
		c, err := relaycfg.Load(*configPath)
		if err != nil {
			return err
		}
		cfg = c
		if cfg.DataDir != "" {
			*dataDir = cfg.DataDir
		}
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	publicHost, _ := st.Setting(ctx, store.SettingPublicHost)
	baseDomain, _ := st.Setting(ctx, store.SettingBaseDomain)
	agentPort, _ := st.Setting(ctx, store.SettingAgentPort)

	fmt.Printf("latch-relay %s\n", Version)
	fmt.Printf("public host:  %s\n", dashIf(publicHost))
	fmt.Printf("base domain:  %s\n", dashIf(baseDomain))
	fmt.Printf("agent port:   %s\n", dashIf(agentPort))
	if cfg != nil {
		mode := "manual TLS"
		if cfg.ACME.Enabled {
			env := "production"
			if cfg.ACME.Staging {
				env = "staging"
			}
			mode = fmt.Sprintf("ACME %s via %s", env, dashIf(cfg.ACME.DNSProvider))
		}
		fmt.Printf("TLS:          %s\n", mode)
		fmt.Printf("listeners:    https=%s http=%s agent=%s\n", cfg.HTTPSAddr, cfg.HTTPAddr, cfg.AgentAddr)
	}

	tokens, _ := st.Tokens(ctx)
	active := 0
	for _, t := range tokens {
		if t.Active(time.Now()) {
			active++
		}
	}
	fmt.Printf("tokens:       %d (%d active)\n", len(tokens), active)

	domains, err := st.ListCustomDomains(ctx)
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		fmt.Println("custom domains: none")
		return nil
	}
	fmt.Println("custom domains:")
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, d := range domains {
		v := "unverified"
		if d.VerifiedAt != nil {
			v = "verified"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", d.Domain, v)
	}
	return tw.Flush()
}

func dashIf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
