package policy

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// trustedSet is a parsed list of trusted proxy prefixes.
type trustedSet struct {
	prefixes []netip.Prefix
}

func parseTrusted(cidrs []string) trustedSet {
	var ts trustedSet
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if addr, err := netip.ParseAddr(c); err == nil {
				ts.prefixes = append(ts.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			}
			continue
		}
		if p, err := netip.ParsePrefix(c); err == nil {
			ts.prefixes = append(ts.prefixes, p)
		}
	}
	return ts
}

func (ts trustedSet) contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range ts.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP resolves the real client IP from the direct peer address and the
// X-Forwarded-For chain, trusting only the configured proxies. It walks XFF
// right-to-left, accepting a forwarded hop only while the address handing it to
// us is trusted; the first untrusted address is the client. This prevents
// spoofing XFF from an untrusted peer.
func ClientIP(remoteAddr string, h http.Header, trusted []string) string {
	ts := parseTrusted(trusted)
	peer := hostOf(remoteAddr)
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil {
		return peer
	}
	// If the direct peer is not trusted, it is the client; ignore XFF.
	if !ts.contains(peerAddr) {
		return peerAddr.Unmap().String()
	}
	hops := forwardedFor(h)
	// Walk from the closest hop (rightmost) outward.
	current := peerAddr
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			// Unparseable hop: stop and use the last good address.
			return current.Unmap().String()
		}
		if !ts.contains(current) {
			return current.Unmap().String()
		}
		current = a
	}
	return current.Unmap().String()
}

func forwardedFor(h http.Header) []string {
	if h == nil {
		return nil
	}
	var out []string
	for _, v := range h.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func hostOf(remoteAddr string) string {
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return h
	}
	return remoteAddr
}
