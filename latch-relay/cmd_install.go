package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/acme"
	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	relaycfg "github.com/kayaramazan/latch/latch-relay/internal/config"
	"github.com/kayaramazan/latch/latch-relay/internal/doctor"
	"github.com/kayaramazan/latch/latch-relay/internal/install"
	"github.com/kayaramazan/latch/proto/tunnel"
)

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	a := install.Answers{}
	fs.StringVar(&a.PublicHost, "public-host", "", "relay hostname, e.g. relay.example.com")
	fs.StringVar(&a.BaseDomain, "base-domain", "", "wildcard zone (default: public-host)")
	fs.StringVar(&a.Email, "email", "", "contact email for Let's Encrypt")
	fs.StringVar(&a.PublicIP, "public-ip", "", "this server's public IP")
	fs.StringVar(&a.DNSProvider, "dns", "", "DNS-01 provider: cloudflare|manual")
	fs.StringVar(&a.Cloudflare, "cloudflare-token", "", "Cloudflare API token")
	fs.BoolVar(&a.Staging, "staging", false, "use Let's Encrypt staging")
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	configOut := fs.String("config-out", relaycfg.DefaultPath, "where to write the config file")
	systemdOut := fs.String("systemd-out", "/etc/systemd/system/latch-relay.service", "where to write the systemd unit")
	binPath := fs.String("bin", selfPath(), "latch-relay binary path for the systemd unit")
	yes := fs.Bool("yes", false, "non-interactive: fail instead of prompting for missing answers")
	noWrite := fs.Bool("dry-run", false, "print what would be written without touching the filesystem")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a.DataDir = *dataDir

	in := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		if *yes {
			return def
		}
		fmt.Printf("%s", prompt)
		if def != "" {
			fmt.Printf(" [%s]", def)
		}
		fmt.Print(": ")
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	if a.PublicHost == "" {
		a.PublicHost = ask("Relay hostname (e.g. relay.example.com)", "")
	}
	if a.BaseDomain == "" {
		a.BaseDomain = ask("Wildcard base domain", a.PublicHost)
	}
	if a.Email == "" {
		a.Email = ask("Email for Let's Encrypt", "")
	}
	if a.PublicIP == "" {
		a.PublicIP = ask("This server's public IP", guessPublicIP())
	}
	if a.DNSProvider == "" {
		a.DNSProvider = ask("DNS-01 provider (cloudflare/manual)", "manual")
	}
	if a.DNSProvider == acme.DNSCloudflare && a.Cloudflare == "" {
		a.Cloudflare = ask("Cloudflare API token", "")
	}

	if err := a.Validate(); err != nil {
		return err
	}

	// DNS precheck (non-fatal warning).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if a.PublicIP != "" {
		c := doctor.DNSCheck(ctx, doctor.DefaultResolver(), install.WildcardProbe(a.BaseDomain), a.PublicIP)
		if !c.OK {
			fmt.Printf("warning: %s — %s\n", c.Name, c.Detail)
			fmt.Printf("         add a wildcard A record *.%s → %s before requesting certificates.\n", a.BaseDomain, a.PublicIP)
		} else {
			fmt.Printf("DNS looks good: *.%s resolves to %s\n", a.BaseDomain, a.PublicIP)
		}
	}

	cfg := a.Config()
	if cfg.DataDir == "" {
		cfg.DataDir = *dataDir
	}
	unit := install.RenderSystemdUnit(install.SystemdOptions{BinPath: *binPath, ConfigPath: *configOut})

	if *noWrite {
		fmt.Printf("\n--- %s ---\n", *configOut)
		b, _ := cfg.Marshal()
		os.Stdout.Write(b)
		fmt.Printf("\n--- %s ---\n%s\n", *systemdOut, unit)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(*configOut), 0o755); err != nil {
		return err
	}
	if err := cfg.Write(*configOut); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("Wrote %s\n", *configOut)
	if err := os.WriteFile(*systemdOut, []byte(unit), 0o644); err != nil {
		fmt.Printf("warning: could not write systemd unit (%v); install it manually\n", err)
	} else {
		fmt.Printf("Wrote %s\n", *systemdOut)
	}

	// First token.
	conn, err := createFirstToken(*dataDir, a)
	if err != nil {
		return fmt.Errorf("create first token: %w", err)
	}
	fmt.Println("\nSetup complete. Next steps:")
	fmt.Println("  sudo systemctl daemon-reload")
	fmt.Println("  sudo systemctl enable --now latch-relay")
	fmt.Println("  latch-relay doctor --config", *configOut)
	fmt.Println("\nPaste this into an agent (shown once):")
	fmt.Println(" ", conn)
	return nil
}

func createFirstToken(dataDir string, a install.Answers) (string, error) {
	st, err := openStore(dataDir)
	if err != nil {
		return "", err
	}
	defer st.Close()
	ctx := context.Background()
	scope := tunnel.Scope{Hostnames: []string{"*." + a.BaseDomain}}
	plain, _, err := auth.New(st).Create(ctx, "first", scope, nil)
	if err != nil {
		return "", err
	}
	ci := tunnel.ConnInfo{Host: a.PublicHost, Port: tunnel.DefaultAgentPort, Token: plain, Name: a.PublicHost}
	return ci.String(), nil
}

// guessPublicIP returns the local outbound IP as a best-effort default.
func guessPublicIP() string {
	conn, err := net.Dial("udp", "203.0.113.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP.String()
	}
	return ""
}

func selfPath() string {
	p, err := os.Executable()
	if err != nil {
		return "/usr/local/bin/latch-relay"
	}
	return p
}
