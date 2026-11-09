package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	relaycfg "github.com/kayaramazan/latch/latch-relay/internal/config"
	"github.com/kayaramazan/latch/latch-relay/internal/doctor"
	"github.com/kayaramazan/latch/latch-relay/internal/install"
)

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", "", "config file to read settings from")
	publicHost := fs.String("public-host", "", "relay hostname (default: from config)")
	baseDomain := fs.String("base-domain", "", "wildcard zone (default: public-host)")
	publicIP := fs.String("public-ip", "", "expected public IP for DNS checks")
	httpsAddr := fs.String("https-addr", ":443", "HTTPS listen address")
	agentAddr := fs.String("agent-addr", ":7443", "agent listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath != "" {
		cfg, err := relaycfg.Load(*configPath)
		if err != nil {
			return err
		}
		if *publicHost == "" {
			*publicHost = cfg.PublicHost
		}
		if *baseDomain == "" {
			*baseDomain = cfg.BaseDomain
		}
		if *publicIP == "" {
			*publicIP = cfg.PublicIP
		}
		*httpsAddr, *agentAddr = cfg.HTTPSAddr, cfg.AgentAddr
	}
	if *publicHost == "" {
		return fmt.Errorf("--public-host is required (or --config)")
	}
	if *baseDomain == "" {
		*baseDomain = *publicHost
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res := doctor.DefaultResolver()

	var checks []doctor.Check
	checks = append(checks, doctor.DNSCheck(ctx, res, *publicHost, *publicIP))
	checks = append(checks, doctor.DNSCheck(ctx, res, install.WildcardProbe(*baseDomain), *publicIP))
	// Ports: if a relay is already running these will be in use; report reachability instead.
	checks = append(checks, portCheck(*httpsAddr))
	checks = append(checks, portCheck(*agentAddr))
	if ref, err := httpDate(ctx, "https://acme-v02.api.letsencrypt.org/directory"); err == nil {
		checks = append(checks, doctor.ClockCheck(time.Now(), ref, 60*time.Second))
	} else {
		checks = append(checks, doctor.Check{Name: "clock", OK: true, Detail: "skipped (no network reference)"})
	}

	for _, c := range checks {
		mark := "ok "
		if !c.OK {
			mark = "FAIL"
			if !c.Fatal {
				mark = "warn"
			}
		}
		fmt.Printf("[%s] %-28s %s\n", mark, c.Name, c.Detail)
	}
	if !doctor.Summarize(checks) {
		return fmt.Errorf("one or more fatal checks failed")
	}
	fmt.Println("all checks passed")
	return nil
}

// portCheck reports a port as free (bindable) or, if in use, reachable.
func portCheck(addr string) doctor.Check {
	if c := doctor.PortFree(addr); c.OK {
		return c
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return doctor.Check{Name: "port:" + addr, Fatal: true, Detail: err.Error()}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return doctor.Reachable(net.DialTimeout, net.JoinHostPort(host, port), 3*time.Second)
}

func httpDate(ctx context.Context, url string) (time.Time, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	return http.ParseTime(resp.Header.Get("Date"))
}
