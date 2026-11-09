package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/kayaramazan/latch/policy"
)

// Explain implements `latchd explain --route <id> --ip <ip> [...]`.
func Explain(args []string) error {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	route := fs.String("route", "", "route id to explain")
	ip := fs.String("ip", "", "sample client IP")
	method := fs.String("method", "GET", "sample HTTP method")
	path := fs.String("path", "/", "sample request path")
	var headers multiFlag
	fs.Var(&headers, "header", "sample header K:V (repeatable)")
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *route == "" {
		return fmt.Errorf("--route is required")
	}
	hdr := map[string][]string{}
	for _, h := range headers {
		if k, v, ok := strings.Cut(h, ":"); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			hdr[k] = append(hdr[k], v)
		}
	}
	body := map[string]any{"routeId": *route, "clientIp": *ip, "method": *method, "path": *path, "headers": hdr}

	c := newAPIClient(*apiFlag)
	var ex policy.Explanation
	if err := c.do("POST", "/api/explain", body, &ex); err != nil {
		return err
	}
	fmt.Printf("route %s (%s), client %s", ex.RouteID, ex.Where, dash(ex.ClientIP))
	if ex.Country != "" {
		fmt.Printf(" [%s]", ex.Country)
	}
	fmt.Println()
	for _, st := range ex.Steps {
		mark := " "
		if st.Matched {
			mark = "*"
		}
		fmt.Printf("  %s %-18s %s\n", mark, st.Stage, st.Detail)
	}
	result := strings.ToUpper(ex.Result)
	if ex.Result == "deny" {
		fmt.Printf("=> %s (%s, rule %s)\n", result, ex.DenyReason, ex.RuleID)
	} else {
		fmt.Printf("=> %s\n", result)
	}
	return nil
}
