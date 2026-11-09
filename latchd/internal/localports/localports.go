// Package localports detects TCP ports that are listening on this machine, so
// the UI can suggest "what are you publishing?" instead of making the user type
// an address. Detection is best-effort and read-only.
package localports

import (
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Port is a detected listening socket.
type Port struct {
	Port    int    `json:"port"`
	Address string `json:"address"` // suggested dial target, e.g. 127.0.0.1:3000
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
}

// List returns listening TCP ports on loopback / all interfaces, de-duplicated
// and sorted. It never fails hard: on any error it returns what it has.
func List() []Port {
	out := runLsof()
	if len(out) == 0 && runtime.GOOS == "windows" {
		out = runNetstat()
	}
	return dedupe(out)
}

// runLsof uses lsof (macOS, most Linux) for machine-readable listener info.
func runLsof() []Port {
	cmd := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseLsof(string(b))
}

// parseLsof parses `lsof -F pcn` output into ports.
func parseLsof(s string) []Port {
	var out []Port
	var pid int
	var cmd string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			cmd = line[1:]
		case 'n':
			if p, ok := portFromName(line[1:]); ok {
				out = append(out, Port{Port: p, Address: dialTarget(line[1:], p), Process: cmd, PID: pid})
			}
		}
	}
	return out
}

// runNetstat is the Windows fallback.
func runNetstat() []Port {
	b, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}
	var out []Port
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		if p, ok := portFromName(f[1]); ok {
			pid, _ := strconv.Atoi(f[len(f)-1])
			out = append(out, Port{Port: p, Address: dialTarget(f[1], p), PID: pid})
		}
	}
	return out
}

// portFromName extracts the port from an address like "*:3000",
// "127.0.0.1:3000", "[::1]:3000" or "localhost:3000".
func portFromName(name string) (int, bool) {
	i := strings.LastIndexByte(name, ':')
	if i < 0 || i == len(name)-1 {
		return 0, false
	}
	p, err := strconv.Atoi(name[i+1:])
	if err != nil || p < 1 || p > 65535 {
		return 0, false
	}
	return p, true
}

// dialTarget returns the address an agent should dial for this listener. A
// wildcard or IPv6-any listener is reachable on 127.0.0.1.
func dialTarget(name string, port int) string {
	host := name[:strings.LastIndexByte(name, ':')]
	switch host {
	case "*", "", "0.0.0.0", "[::]", "::", "[::1]", "::1":
		return "127.0.0.1:" + strconv.Itoa(port)
	}
	host = strings.Trim(host, "[]")
	return host + ":" + strconv.Itoa(port)
}

// dedupe collapses duplicate ports (lsof lists IPv4+IPv6 separately), prefers a
// named process, and sorts by port.
func dedupe(in []Port) []Port {
	best := map[int]Port{}
	for _, p := range in {
		cur, ok := best[p.Port]
		if !ok || (cur.Process == "" && p.Process != "") {
			best[p.Port] = p
		}
	}
	out := make([]Port, 0, len(best))
	for _, p := range best {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}
