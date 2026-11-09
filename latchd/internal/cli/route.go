package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/kayaramazan/latch/proto/model"
)

// Route dispatches `latchd route ...`.
func Route(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: latchd route <add|list|rm> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return routeAdd(rest)
	case "list", "ls":
		return routeList(rest)
	case "rm", "remove", "delete":
		return routeRm(rest)
	default:
		return fmt.Errorf("unknown route subcommand %q", sub)
	}
}

func routeAdd(args []string) error {
	fs := flag.NewFlagSet("route add", flag.ContinueOnError)
	target := fs.String("target", "", "local target host:port, e.g. 127.0.0.1:3000")
	relayRef := fs.String("tunnel", "", "relay id or name to expose through")
	var hostnames multiFlag
	fs.Var(&hostnames, "hostname", "public hostname (repeatable, or comma-separated)")
	name := fs.String("name", "", "display name (default: first hostname)")
	path := fs.String("path", "", "path prefix (default /)")
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || *relayRef == "" || len(hostnames) == 0 {
		return fmt.Errorf("--target, --tunnel and at least one --hostname are required")
	}
	c := newAPIClient(*apiFlag)
	relayID, err := resolveRelay(c, *relayRef)
	if err != nil {
		return err
	}
	displayName := *name
	if displayName == "" {
		displayName = hostnames[0]
	}
	in := routeInput{
		Name:      ptr(displayName),
		Target:    ptr(*target),
		Exposure:  ptr(model.ExposureTunnel),
		Kind:      ptr(model.KindHTTP),
		RelayID:   ptr(relayID),
		Hostnames: ptr([]string(hostnames)),
	}
	if *path != "" {
		in.PathPrefix = ptr(*path)
	}
	var out model.Route
	if err := c.do("POST", "/api/routes", in, &out); err != nil {
		return err
	}
	fmt.Printf("added route %s (%s): %s → %s\n", out.Name, out.ID, strings.Join(out.Hostnames, ", "), out.Target)
	return nil
}

func routeList(args []string) error {
	fs := flag.NewFlagSet("route list", flag.ContinueOnError)
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := newAPIClient(*apiFlag)
	var routes []model.Route
	if err := c.do("GET", "/api/routes", nil, &routes); err != nil {
		return err
	}
	if len(routes) == 0 {
		fmt.Println("no routes configured")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tEXPOSURE\tKIND\tENABLED\tENDPOINT\tTARGET")
	for _, r := range routes {
		endpoint := strings.Join(r.Hostnames, ",")
		if r.Kind == model.KindTCP {
			endpoint = fmt.Sprintf("tcp/%d", r.TCPPort)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\t%s\n", r.ID, r.Name, r.Exposure, r.Kind, r.Enabled, dash(endpoint), r.Target)
	}
	return tw.Flush()
}

func routeRm(args []string) error {
	fs := flag.NewFlagSet("route rm", flag.ContinueOnError)
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: latchd route rm <route-id>")
	}
	c := newAPIClient(*apiFlag)
	if err := c.do("DELETE", "/api/routes/"+fs.Arg(0), nil, nil); err != nil {
		return err
	}
	fmt.Printf("removed route %s\n", fs.Arg(0))
	return nil
}

// multiFlag collects a repeatable string flag; comma-separated values split.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*m = append(*m, p)
		}
	}
	return nil
}
