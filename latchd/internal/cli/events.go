package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

// Events implements `latchd events [--follow] [-n N]`.
func Events(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "stream new events as they arrive")
	n := fs.Int("n", 50, "number of recent events to show")
	route := fs.String("route", "", "filter by route id")
	apiFlag := fs.String("api", "", "latchd API base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := newAPIClient(*apiFlag)
	if !*follow {
		var events []model.AccessEvent
		path := fmt.Sprintf("/api/events?n=%d", *n)
		if *route != "" {
			path += "&route=" + *route
		}
		if err := c.do("GET", path, nil, &events); err != nil {
			return err
		}
		for _, ev := range events {
			fmt.Println(formatEvent(ev))
		}
		return nil
	}
	// Follow: read the SSE stream.
	streamPath := "/api/events/stream"
	if *route != "" {
		streamPath += "?route=" + *route
	}
	req, err := http.NewRequest("GET", c.base+streamPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach latchd at %s (is it running?): %w", c.base, err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev model.AccessEvent
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
			fmt.Println(formatEvent(ev))
		}
	}
	return sc.Err()
}

func formatEvent(ev model.AccessEvent) string {
	ts := ev.TS.Local().Format(time.RFC3339)
	decision := string(ev.Decision)
	if ev.Decision == model.DecisionDeny {
		decision = "DENY(" + ev.DenyReason + ")"
	}
	country := ""
	if ev.Country != "" {
		country = " " + ev.Country
	}
	src := string(ev.Source)
	if src == "" {
		src = "?"
	}
	return fmt.Sprintf("%s  [%-5s] %-18s %s%s  %s %s -> %d  %s",
		ts, src, ev.Hostname, ev.ClientIP, country, dash(ev.Method), dash(ev.Path), ev.Status, decision)
}
