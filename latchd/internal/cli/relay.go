package cli

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Relay dispatches `latchd relay ...`.
func Relay(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: latchd relay <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return relayAdd(rest)
	case "list", "ls":
		return relayList(rest)
	case "rm", "remove", "delete":
		return relayRm(rest)
	default:
		return fmt.Errorf("unknown relay subcommand %q", sub)
	}
}

func relayAdd(args []string) error {
	fs := flag.NewFlagSet("relay add", flag.ContinueOnError)
	url := fs.String("url", "", "connection string: latch://host:port/?token=...&ca=...")
	name := fs.String("name", "", "display name")
	host := fs.String("host", "", "relay host (instead of --url)")
	port := fs.Int("port", 0, "relay agent port (default 7443)")
	token := fs.String("token", "", "relay token (instead of --url)")
	caFile := fs.String("ca-file", "", "PEM CA file to trust (dev relays)")
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rl := relayInput{}
	if *url != "" {
		info, err := tunnel.ParseConnString(*url)
		if err != nil {
			return err
		}
		rl.Host = ptr(info.Host)
		rl.Port = ptr(info.Port)
		rl.Token = ptr(info.Token)
		if info.CAPEM != "" {
			rl.CAPEM = ptr(info.CAPEM)
		}
		if info.Name != "" {
			rl.Name = ptr(info.Name)
		}
	}
	if *host != "" {
		rl.Host = ptr(*host)
	}
	if *port != 0 {
		rl.Port = ptr(*port)
	}
	if *token != "" {
		rl.Token = ptr(*token)
	}
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			return fmt.Errorf("read ca-file: %w", err)
		}
		rl.CAPEM = ptr(string(pem))
	}
	if *name != "" {
		rl.Name = ptr(*name)
	}
	if rl.Name == nil && rl.Host != nil {
		rl.Name = ptr(*rl.Host)
	}
	if rl.Host == nil || rl.Token == nil {
		return fmt.Errorf("provide --url, or --host and --token")
	}

	c := newAPIClient(*apiFlag)
	var out model.Relay
	if err := c.do("POST", "/api/relays", rl, &out); err != nil {
		return err
	}
	fmt.Printf("added relay %s (%s) → %s\n", out.Name, out.ID, hostPort(out.Host, out.Port))
	return nil
}

func relayList(args []string) error {
	fs := flag.NewFlagSet("relay list", flag.ContinueOnError)
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := newAPIClient(*apiFlag)
	var relays []model.Relay
	if err := c.do("GET", "/api/relays", nil, &relays); err != nil {
		return err
	}
	if len(relays) == 0 {
		fmt.Println("no relays configured")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tADDRESS\tBASE DOMAIN")
	for _, r := range relays {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Name, hostPort(r.Host, r.Port), dash(r.BaseDomain))
	}
	return tw.Flush()
}

func relayRm(args []string) error {
	fs := flag.NewFlagSet("relay rm", flag.ContinueOnError)
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: latchd relay rm <relay-id|name>")
	}
	c := newAPIClient(*apiFlag)
	id, err := resolveRelay(c, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.do("DELETE", "/api/relays/"+id, nil, nil); err != nil {
		return err
	}
	fmt.Printf("removed relay %s\n", id)
	return nil
}

// resolveRelay maps a relay id or name to its id.
func resolveRelay(c *apiClient, ref string) (string, error) {
	var relays []model.Relay
	if err := c.do("GET", "/api/relays", nil, &relays); err != nil {
		return "", err
	}
	for _, r := range relays {
		if r.ID == ref {
			return r.ID, nil
		}
	}
	var matches []model.Relay
	for _, r := range relays {
		if r.Name == ref {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no relay with id or name %q", ref)
	case 1:
		return matches[0].ID, nil
	default:
		return "", fmt.Errorf("%d relays named %q; use the relay id", len(matches), ref)
	}
}

func ptr[T any](v T) *T { return &v }

func hostPort(host string, port int) string {
	if port == 0 {
		port = tunnel.DefaultAgentPort
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
