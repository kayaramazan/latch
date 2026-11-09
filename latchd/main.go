// Command latchd is the Latch agent: a local reverse proxy (embedded Caddy)
// and tunnel client, managed through a local REST/WebSocket API and web UI.
package main

import (
	"fmt"
	"os"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("latchd", Version)
		return
	}
	fmt.Fprintln(os.Stderr, "latchd: not implemented yet (see ROADMAP.md, phase 3)")
	os.Exit(2)
}
