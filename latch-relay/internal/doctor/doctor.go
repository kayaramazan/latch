// Package doctor runs environment checks for the relay: DNS, listening ports,
// certificate and clock. The decision logic is pure and unit-tested; the CLI
// wires in real network probes.
package doctor

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Fatal  bool // a failing fatal check means the relay cannot work
}

// Resolver abstracts DNS lookups (net.DefaultResolver in production).
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DNSCheck verifies that name resolves to wantIP. When wantIP is empty it only
// checks that the name resolves at all.
func DNSCheck(ctx context.Context, r Resolver, name, wantIP string) Check {
	c := Check{Name: "dns:" + name, Fatal: true}
	addrs, err := r.LookupHost(ctx, name)
	if err != nil || len(addrs) == 0 {
		c.Detail = fmt.Sprintf("%s does not resolve", name)
		return c
	}
	if wantIP == "" {
		c.OK = true
		c.Detail = fmt.Sprintf("resolves to %v", addrs)
		return c
	}
	for _, a := range addrs {
		if a == wantIP {
			c.OK = true
			c.Detail = fmt.Sprintf("resolves to %s", wantIP)
			return c
		}
	}
	c.Detail = fmt.Sprintf("resolves to %v, expected %s (check your DNS records)", addrs, wantIP)
	return c
}

// ClockCheck verifies the local clock is within tolerance of a reference time.
// ACME fails if the clock is far off.
func ClockCheck(now, ref time.Time, tolerance time.Duration) Check {
	c := Check{Name: "clock"}
	skew := now.Sub(ref)
	if skew < 0 {
		skew = -skew
	}
	if skew <= tolerance {
		c.OK = true
		c.Detail = fmt.Sprintf("skew %s within %s", skew.Round(time.Millisecond), tolerance)
		return c
	}
	c.Fatal = true
	c.Detail = fmt.Sprintf("clock is off by %s (tolerance %s); fix NTP", skew.Round(time.Second), tolerance)
	return c
}

// Dialer abstracts a TCP dial (net.DialTimeout in production).
type Dialer func(network, addr string, timeout time.Duration) (net.Conn, error)

// PortFree reports whether the relay can bind addr. A failure usually means
// another process (or an old relay) holds the port.
func PortFree(addr string) Check {
	c := Check{Name: "port:" + addr, Fatal: true}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		c.Detail = fmt.Sprintf("cannot bind %s: %v", addr, err)
		return c
	}
	_ = ln.Close()
	c.OK = true
	c.Detail = "bindable"
	return c
}

// Reachable reports whether addr accepts TCP connections (used to confirm a
// running relay is listening, or that a firewall allows a port).
func Reachable(dial Dialer, addr string, timeout time.Duration) Check {
	c := Check{Name: "reachable:" + addr}
	conn, err := dial("tcp", addr, timeout)
	if err != nil {
		c.Detail = fmt.Sprintf("cannot reach %s: %v", addr, err)
		return c
	}
	_ = conn.Close()
	c.OK = true
	c.Detail = "reachable"
	return c
}

// Summarize returns whether every fatal check passed.
func Summarize(checks []Check) (ok bool) {
	ok = true
	for _, c := range checks {
		if c.Fatal && !c.OK {
			ok = false
		}
	}
	return ok
}

// DefaultResolver returns a Resolver backed by net.DefaultResolver.
func DefaultResolver() Resolver { return net.DefaultResolver }
