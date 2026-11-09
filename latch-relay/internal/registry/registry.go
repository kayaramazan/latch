// Package registry maps public hostnames and TCP ports to the agent sessions
// that serve them. Ownership belongs to a token, is persisted in the relay
// store and survives agent disconnects: an offline route stays known so the
// relay can answer 503 and keep enforcing policies.
package registry

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Peer is a connected agent session. Sessions implement this; the registry
// never imports the session package.
type Peer interface {
	SessionID() string
	TokenID() string
	AgentID() string
	// OpenStream opens a data stream to the agent, writes the header and
	// returns the stream once the agent reported StatusOK.
	OpenStream(ctx context.Context, h *tunnel.StreamHeader) (net.Conn, error)
}

// Entry is one published route. Peer is nil while the agent is offline.
type Entry struct {
	RouteID      string
	TokenID      string
	AgentID      string
	Kind         model.RouteKind
	Hostname     string
	PathPrefix   string
	Port         int
	Peer         Peer
	LastOnlineAt time.Time
}

// Online reports whether an agent currently serves the entry.
func (e *Entry) Online() bool { return e != nil && e.Peer != nil }

// Options configure a Registry.
type Options struct {
	PublicHost   string // relay hostname used in tcp:// URLs
	HTTPSPort    int    // public HTTPS port, for URLs (443 omitted)
	TCPPortRange [2]int // pool for TCP tunnels
	// OnTCPChange is called (outside the lock) when a TCP port gains or loses
	// an entry; e is nil when the port is released. Used by the ingress to
	// start/stop listeners.
	OnTCPChange func(port int, e *Entry)
}

type hostKey struct{ host, prefix string }

// Registry is safe for concurrent use.
type Registry struct {
	st   *store.Store
	opts Options

	mu   sync.RWMutex
	http map[hostKey]*Entry
	tcp  map[int]*Entry
}

// New creates an empty registry; call Load to restore persisted routes.
func New(st *store.Store, opts Options) *Registry {
	if opts.TCPPortRange == [2]int{} {
		opts.TCPPortRange = [2]int{20000, 20999}
	}
	if opts.HTTPSPort == 0 {
		opts.HTTPSPort = 443
	}
	return &Registry{st: st, opts: opts, http: map[hostKey]*Entry{}, tcp: map[int]*Entry{}}
}

// Load restores routes from the store as offline entries.
func (r *Registry) Load(ctx context.Context) error {
	hr, err := r.st.HTTPRoutes(ctx)
	if err != nil {
		return err
	}
	tr, err := r.st.TCPRoutes(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	for _, x := range hr {
		r.http[hostKey{x.Hostname, x.PathPrefix}] = &Entry{RouteID: x.RouteID, TokenID: x.TokenID, AgentID: x.AgentID,
			Kind: model.KindHTTP, Hostname: x.Hostname, PathPrefix: x.PathPrefix, LastOnlineAt: x.LastOnlineAt}
	}
	for _, x := range tr {
		r.tcp[x.Port] = &Entry{RouteID: x.RouteID, TokenID: x.TokenID, AgentID: x.AgentID,
			Kind: model.KindTCP, Port: x.Port, LastOnlineAt: x.LastOnlineAt}
	}
	r.mu.Unlock()
	for _, x := range tr {
		r.notifyTCP(x.Port)
	}
	return nil
}

// Announce applies an agent's full route set (PROTOCOL.md: routes not listed
// are withdrawn) and returns the ack.
func (r *Registry) Announce(ctx context.Context, p Peer, scope tunnel.Scope, specs []tunnel.RouteSpec) tunnel.RouteAck {
	ack := tunnel.RouteAck{Accepted: []tunnel.RouteAccepted{}, Rejected: []tunnel.RouteRejected{}}
	var changedPorts []int

	r.mu.Lock()
	keep := map[string]bool{}
	seenInBatch := map[hostKey]bool{}
	for _, spec := range specs {
		spec.PathPrefix = normalizePrefix(spec.PathPrefix)
		if spec.Kind == "" {
			spec.Kind = model.KindHTTP
		}
		if code, msg := r.checkSpec(p, scope, &spec, seenInBatch); code != "" {
			ack.Rejected = append(ack.Rejected, tunnel.RouteRejected{RouteID: spec.RouteID, Code: code, Message: msg})
			continue
		}
		switch spec.Kind {
		case model.KindHTTP:
			acc := tunnel.RouteAccepted{RouteID: spec.RouteID, PublicURLs: []string{}}
			for _, h := range spec.Hostnames {
				h = strings.ToLower(h)
				k := hostKey{h, spec.PathPrefix}
				seenInBatch[k] = true
				e := &Entry{RouteID: spec.RouteID, TokenID: p.TokenID(), AgentID: p.AgentID(), Kind: model.KindHTTP,
					Hostname: h, PathPrefix: spec.PathPrefix, Peer: p, LastOnlineAt: time.Now().UTC()}
				r.http[k] = e
				_ = r.st.UpsertHTTPRoute(ctx, &store.HTTPRoute{Hostname: h, PathPrefix: spec.PathPrefix,
					TokenID: p.TokenID(), AgentID: p.AgentID(), RouteID: spec.RouteID})
				acc.Hostnames = append(acc.Hostnames, h)
				acc.PublicURLs = append(acc.PublicURLs, r.httpsURL(h, spec.PathPrefix))
			}
			ack.Accepted = append(ack.Accepted, acc)
		case model.KindTCP:
			port := r.pickPort(p, spec)
			e := &Entry{RouteID: spec.RouteID, TokenID: p.TokenID(), AgentID: p.AgentID(), Kind: model.KindTCP,
				Port: port, Peer: p, LastOnlineAt: time.Now().UTC()}
			r.tcp[port] = e
			_ = r.st.UpsertTCPRoute(ctx, &store.TCPRoute{Port: port, TokenID: p.TokenID(), AgentID: p.AgentID(), RouteID: spec.RouteID})
			changedPorts = append(changedPorts, port)
			ack.Accepted = append(ack.Accepted, tunnel.RouteAccepted{RouteID: spec.RouteID, TCPPort: port,
				PublicURLs: []string{"tcp://" + net.JoinHostPort(r.opts.PublicHost, strconv.Itoa(port))}})
		}
		keep[spec.RouteID] = true
	}
	// Full-set semantics: drop this agent's routes that were not announced.
	for k, e := range r.http {
		if e.TokenID == p.TokenID() && e.AgentID == p.AgentID() && !keep[e.RouteID] {
			delete(r.http, k)
			_ = r.st.DeleteHTTPRoute(ctx, k.host, k.prefix)
		}
	}
	for port, e := range r.tcp {
		if e.TokenID == p.TokenID() && e.AgentID == p.AgentID() && !keep[e.RouteID] {
			delete(r.tcp, port)
			_ = r.st.DeleteTCPRoute(ctx, port)
			changedPorts = append(changedPorts, port)
		}
	}
	r.mu.Unlock()

	for _, port := range changedPorts {
		r.notifyTCP(port)
	}
	return ack
}

// checkSpec validates a route against the scope and current ownership.
// Caller holds r.mu.
func (r *Registry) checkSpec(p Peer, scope tunnel.Scope, spec *tunnel.RouteSpec, seen map[hostKey]bool) (code, msg string) {
	if spec.RouteID == "" {
		return tunnel.ErrInvalid, "routeId is required"
	}
	switch spec.Kind {
	case model.KindHTTP:
		if len(spec.Hostnames) == 0 {
			return tunnel.ErrInvalid, "http routes need at least one hostname"
		}
		if !strings.HasPrefix(spec.PathPrefix, "/") {
			return tunnel.ErrInvalid, "pathPrefix must start with /"
		}
		for _, h := range spec.Hostnames {
			h = strings.ToLower(h)
			if err := model.ValidateHostname(h); err != nil {
				return tunnel.ErrInvalid, fmt.Sprintf("hostname %q: %v", h, err)
			}
			if !auth.ScopeAllows(scope, h) {
				return tunnel.ErrScope, fmt.Sprintf("hostname %q is not in the token scope", h)
			}
			k := hostKey{h, spec.PathPrefix}
			if seen[k] {
				return tunnel.ErrInvalid, fmt.Sprintf("hostname %q announced twice", h)
			}
			if cur, ok := r.http[k]; ok && cur.TokenID != p.TokenID() {
				return tunnel.ErrHostnameTaken, fmt.Sprintf("hostname %q belongs to another token", h)
			}
		}
	case model.KindTCP:
		if !scope.TCP {
			return tunnel.ErrTCPNotAllowed, "token scope does not allow tcp tunnels"
		}
		if spec.TCPPort != 0 {
			if !r.inRange(spec.TCPPort) {
				return tunnel.ErrTCPPortUnavailable, fmt.Sprintf("port %d is outside the relay pool %d-%d", spec.TCPPort, r.opts.TCPPortRange[0], r.opts.TCPPortRange[1])
			}
			if cur, ok := r.tcp[spec.TCPPort]; ok && cur.TokenID != p.TokenID() {
				return tunnel.ErrTCPPortUnavailable, fmt.Sprintf("port %d is in use", spec.TCPPort)
			}
		} else if r.freePort(p, spec.RouteID) == 0 {
			return tunnel.ErrTCPPortUnavailable, "no free port in the relay pool"
		}
	default:
		return tunnel.ErrInvalid, fmt.Sprintf("unknown kind %q", spec.Kind)
	}
	return "", ""
}

func (r *Registry) inRange(port int) bool {
	return port >= r.opts.TCPPortRange[0] && port <= r.opts.TCPPortRange[1]
}

// freePort returns the port a route previously used (stable URLs across
// reconnects) or the lowest free port; 0 if the pool is exhausted. Caller
// holds r.mu.
func (r *Registry) freePort(p Peer, routeID string) int {
	for port, e := range r.tcp {
		if e.TokenID == p.TokenID() && e.RouteID == routeID {
			return port
		}
	}
	for port := r.opts.TCPPortRange[0]; port <= r.opts.TCPPortRange[1]; port++ {
		if _, used := r.tcp[port]; !used {
			return port
		}
	}
	return 0
}

func (r *Registry) pickPort(p Peer, spec tunnel.RouteSpec) int {
	if spec.TCPPort != 0 {
		return spec.TCPPort
	}
	return r.freePort(p, spec.RouteID)
}

// Withdraw removes specific routes of a peer.
func (r *Registry) Withdraw(ctx context.Context, p Peer, routeIDs []string) {
	drop := map[string]bool{}
	for _, id := range routeIDs {
		drop[id] = true
	}
	var changedPorts []int
	r.mu.Lock()
	for k, e := range r.http {
		if e.TokenID == p.TokenID() && drop[e.RouteID] {
			delete(r.http, k)
			_ = r.st.DeleteHTTPRoute(ctx, k.host, k.prefix)
		}
	}
	for port, e := range r.tcp {
		if e.TokenID == p.TokenID() && drop[e.RouteID] {
			delete(r.tcp, port)
			_ = r.st.DeleteTCPRoute(ctx, port)
			changedPorts = append(changedPorts, port)
		}
	}
	r.mu.Unlock()
	for _, port := range changedPorts {
		r.notifyTCP(port)
	}
}

// Detach marks every route served by the peer offline (the agent
// disconnected). Routes stay registered.
func (r *Registry) Detach(ctx context.Context, p Peer) {
	now := time.Now().UTC()
	r.mu.Lock()
	for _, e := range r.http {
		if e.Peer != nil && e.Peer.SessionID() == p.SessionID() {
			e.Peer, e.LastOnlineAt = nil, now
		}
	}
	for _, e := range r.tcp {
		if e.Peer != nil && e.Peer.SessionID() == p.SessionID() {
			e.Peer, e.LastOnlineAt = nil, now
		}
	}
	r.mu.Unlock()
	_ = r.st.MarkOffline(ctx, p.TokenID(), p.AgentID())
}

// DropToken removes every route of a token (after revocation).
func (r *Registry) DropToken(tokenID string) {
	var changedPorts []int
	r.mu.Lock()
	for k, e := range r.http {
		if e.TokenID == tokenID {
			delete(r.http, k)
		}
	}
	for port, e := range r.tcp {
		if e.TokenID == tokenID {
			delete(r.tcp, port)
			changedPorts = append(changedPorts, port)
		}
	}
	r.mu.Unlock()
	for _, port := range changedPorts {
		r.notifyTCP(port)
	}
}

// SetHTTPSPort updates the public port used in URLs (listeners bound to :0).
func (r *Registry) SetHTTPSPort(port int) {
	r.mu.Lock()
	r.opts.HTTPSPort = port
	r.mu.Unlock()
}

// LookupHTTP finds the entry with the longest matching path prefix for the
// hostname, or nil. The returned Entry is a copy.
func (r *Registry) LookupHTTP(hostname, path string) *Entry {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	if path == "" {
		path = "/"
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best *Entry
	for k, e := range r.http {
		if k.host != hostname || !prefixMatches(k.prefix, path) {
			continue
		}
		if best == nil || len(k.prefix) > len(best.PathPrefix) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	c := *best
	return &c
}

// LookupTCP returns a copy of the entry for a port, or nil.
func (r *Registry) LookupTCP(port int) *Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.tcp[port]
	if !ok {
		return nil
	}
	c := *e
	return &c
}

// Entries returns copies of every entry, sorted for stable output.
func (r *Registry) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.http)+len(r.tcp))
	for _, e := range r.http {
		out = append(out, *e)
	}
	for _, e := range r.tcp {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hostname != out[j].Hostname {
			return out[i].Hostname < out[j].Hostname
		}
		if out[i].PathPrefix != out[j].PathPrefix {
			return out[i].PathPrefix < out[j].PathPrefix
		}
		return out[i].Port < out[j].Port
	})
	return out
}

func (r *Registry) notifyTCP(port int) {
	if r.opts.OnTCPChange == nil {
		return
	}
	r.opts.OnTCPChange(port, r.LookupTCP(port))
}

func (r *Registry) httpsURL(host, prefix string) string {
	u := "https://" + host
	if r.opts.HTTPSPort != 443 {
		u += ":" + strconv.Itoa(r.opts.HTTPSPort)
	}
	if prefix != "/" {
		return u + prefix + "/"
	}
	return u + "/"
}

func normalizePrefix(p string) string {
	if p == "" {
		return "/"
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// prefixMatches reports whether path is under prefix on a segment boundary.
func prefixMatches(prefix, path string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
