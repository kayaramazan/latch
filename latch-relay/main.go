// Command latch-relay is the self-hosted Latch relay: it terminates TLS for
// public hostnames and forwards traffic to connected agents over tunnels.
package main

import (
	"fmt"
	"os"
)

// Version is set at build time via -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("latch-relay", Version)
		return
	}
	fmt.Fprintln(os.Stderr, "latch-relay: not implemented yet (see ROADMAP.md, phase 2)")
	os.Exit(2)
}
