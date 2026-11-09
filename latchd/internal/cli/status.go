package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/tunnel"
)

type statusResponse struct {
	Version       string                `json:"version"`
	ConfigVersion int                   `json:"configVersion"`
	Relays        []tunnel.ClientStatus `json:"relays"`
}

// Status implements `latchd status`.
func Status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := newAPIClient(*apiFlag)
	var st statusResponse
	if err := c.do("GET", "/api/status", nil, &st); err != nil {
		return err
	}
	fmt.Printf("latchd %s  (config v%d)\n", st.Version, st.ConfigVersion)
	if len(st.Relays) == 0 {
		fmt.Println("no relays configured")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RELAY\tSTATE\tADDRESS\tRTT\tROUTES\tUPTIME\tDETAIL")
	for _, r := range st.Relays {
		detail := r.LastError
		if r.State == tunnel.StateConnected {
			detail = strings.Join(r.Accepted, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.RelayName, r.State, r.Addr, rtt(r.RTTMs), r.Routes, uptime(r.ConnectedAt), dash(detail))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, r := range st.Relays {
		for _, u := range r.PublicURLs {
			fmt.Printf("  %s → %s\n", r.RelayName, u)
		}
	}
	return nil
}

func rtt(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", ms)
}

func uptime(since time.Time) string {
	if since.IsZero() {
		return "-"
	}
	d := time.Since(since).Round(time.Second)
	return d.String()
}
