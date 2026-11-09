// Package ingress is the relay's public side: the HTTPS/HTTP handlers that
// route by hostname to agent sessions, the TCP tunnel listeners, the error
// pages and the connection limits.
package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/registry"
	"github.com/kayaramazan/latch/latch-relay/internal/session"
	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Defaults from PROTOCOL.md and docs/SECURITY.md.
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	DefaultMaxHeaderBytes    = 64 << 10
	DefaultMaxConns          = 65536
	DefaultMaxConnsPerIP     = 256
	upstreamHeaderTimeout    = 60 * time.Second
)

// Options configure an Ingress.
type Options struct {
	Registry *registry.Registry
	Logger   *slog.Logger
	// OnEvent receives one AccessEvent per request/connection (may be nil);
	// used for metrics and the local event store.
	OnEvent func(model.AccessEvent)
	// OnAgentEvent delivers an event to the agent that owns the route (nil to
	// skip). The peer comes from the registry entry.
	OnAgentEvent func(peer registry.Peer, ev model.AccessEvent)
	// Policy evaluates relay-side policies (nil => allow everything).
	Policy PolicyEvaluator
	// TrustedProxies is the relay's trusted-proxy list for client-IP resolution.
	TrustedProxies []string
	// TCPBindHost is the host part for TCP tunnel listeners ("" = all).
	TCPBindHost string
	// HTTPSPort is used to build redirect URLs (443 omitted).
	HTTPSPort int
}

// PolicyEvaluator is the relay policy layer (implemented by internal/policies).
type PolicyEvaluator interface {
	Evaluate(tokenID, routeID string, req policy.Request, now time.Time) policy.Decision
	ApplyRequest(tokenID, routeID string, h http.Header)
	ApplyResponse(tokenID, routeID string, h http.Header)
}

// Ingress is safe for concurrent use.
type Ingress struct {
	reg   *registry.Registry
	opts  Options
	log   *slog.Logger
	proxy *httputil.ReverseProxy

	tcpMu sync.Mutex
	tcp   map[int]net.Listener
	wg    sync.WaitGroup
}

// New builds an Ingress.
func New(opts Options) *Ingress {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HTTPSPort == 0 {
		opts.HTTPSPort = 443
	}
	in := &Ingress{opts: opts, reg: opts.Registry, log: opts.Logger, tcp: map[int]net.Listener{}}
	transport := &http.Transport{
		DialContext:            in.dialTunnel,
		DisableKeepAlives:      true, // one data stream per request keeps StreamHeader accurate
		ForceAttemptHTTP2:      false,
		ResponseHeaderTimeout:  upstreamHeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: DefaultMaxHeaderBytes,
	}
	in.proxy = &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "tunnel"})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			if ip, _, err := net.SplitHostPort(pr.In.RemoteAddr); err == nil {
				pr.Out.Header.Set("X-Real-IP", ip)
			}
		},
		ErrorHandler:   in.proxyError,
		ModifyResponse: in.modifyResponse,
		ErrorLog:       nil,
	}
	return in
}

// modifyResponse applies a route's response-phase header rules.
func (in *Ingress) modifyResponse(resp *http.Response) error {
	if in.opts.Policy == nil || resp.Request == nil {
		return nil
	}
	if di, ok := resp.Request.Context().Value(dialKey{}).(*dialInfo); ok && di.tokenID != "" {
		in.opts.Policy.ApplyResponse(di.tokenID, di.routeID, resp.Header)
	}
	return nil
}

// SetHTTPSPort updates the port used in redirects (listeners bound to :0).
func (in *Ingress) SetHTTPSPort(port int) { in.opts.HTTPSPort = port }

// SetRegistry wires the registry after construction (the registry needs the
// ingress TCP callback and vice versa).
func (in *Ingress) SetRegistry(r *registry.Registry) { in.reg = r }

// --- HTTP ---

type dialKey struct{}

type dialInfo struct {
	peer    registry.Peer
	hdr     *tunnel.StreamHeader
	tokenID string
	routeID string
}

func (in *Ingress) dialTunnel(ctx context.Context, _, _ string) (net.Conn, error) {
	di, _ := ctx.Value(dialKey{}).(*dialInfo)
	if di == nil {
		return nil, errors.New("ingress: no tunnel target in context")
	}
	return di.peer.OpenStream(ctx, di.hdr)
}

// ServeHTTP routes a public request to the agent that announced its host.
func (in *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	host := hostOnly(r.Host)
	clientIP, clientPort := splitRemote(r.RemoteAddr)
	ev := model.AccessEvent{TS: start.UTC(), Source: model.SourceRelay, Hostname: host, ClientIP: clientIP,
		Method: r.Method, Path: r.URL.Path, Where: "relay", Decision: model.DecisionAllow}

	e := in.reg.LookupHTTP(host, r.URL.Path)
	if e == nil {
		in.deny(w, &ev, http.StatusNotFound, model.DenyUnknownHost, "Unknown host",
			"No tunnel is registered for this hostname on this relay.")
		return
	}
	ev.RouteID = e.RouteID
	ev.RelayID = ""
	if !e.Online() {
		w.Header().Set("Retry-After", "10")
		in.deny(w, &ev, http.StatusServiceUnavailable, model.DenyAgentOffline, "Agent offline",
			"The agent that serves this hostname is not connected to the relay right now. It will reconnect automatically.")
		return
	}

	// Relay-side policy: resolve the real client IP and decide before the
	// request ever reaches the agent.
	if in.opts.Policy != nil {
		req := policy.Request{RemoteAddr: r.RemoteAddr, Header: r.Header, Method: r.Method, Path: r.URL.Path, Host: host}
		d := in.opts.Policy.Evaluate(e.TokenID, e.RouteID, req, start)
		if d.ClientIP != "" {
			ev.ClientIP, clientIP = d.ClientIP, d.ClientIP
		}
		ev.Country = d.Country
		if !d.Allow {
			in.denyPolicy(w, &ev, e.Peer, d)
			return
		}
		if d.DelayMs > 0 {
			select {
			case <-time.After(time.Duration(d.DelayMs) * time.Millisecond):
			case <-r.Context().Done():
			}
		}
		in.opts.Policy.ApplyRequest(e.TokenID, e.RouteID, r.Header)
	}

	hdr := &tunnel.StreamHeader{RouteID: e.RouteID, Kind: model.KindHTTP, Hostname: host, ClientIP: clientIP,
		ClientPort: clientPort, TLS: r.TLS != nil, StreamID: newStreamID()}
	if r.TLS != nil {
		hdr.SNI = r.TLS.ServerName
	}
	ctx := context.WithValue(r.Context(), dialKey{}, &dialInfo{peer: e.Peer, hdr: hdr, tokenID: e.TokenID, routeID: e.RouteID})
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	in.proxy.ServeHTTP(rec, r.WithContext(ctx))

	ev.Status = rec.status
	ev.BytesOut = rec.bytes
	ev.BytesIn = r.ContentLength
	if ev.BytesIn < 0 {
		ev.BytesIn = 0
	}
	ev.DurationMs = time.Since(start).Milliseconds()
	if rec.denyReason != "" {
		ev.Decision, ev.DenyReason = model.DecisionDeny, rec.denyReason
	}
	in.emit(ev)
	in.toAgent(e.Peer, ev)
}

// denyPolicy answers a policy denial (status page or connection drop) and
// records the event, including on the agent's event stream.
func (in *Ingress) denyPolicy(w http.ResponseWriter, ev *model.AccessEvent, peer registry.Peer, d policy.Decision) {
	ev.Decision, ev.DenyReason, ev.RuleID = model.DecisionDeny, d.DenyReason, d.RuleID
	ev.DurationMs = time.Since(ev.TS).Milliseconds()
	if d.Drop {
		ev.Status = 0
		if hj, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			_ = hj
		}
		if rc := http.NewResponseController(w); rc != nil {
			if conn, _, err := rc.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		in.emit(*ev)
		in.toAgent(peer, *ev)
		return
	}
	status := d.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	if d.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(d.RetryAfter))
	}
	title, text := policyDenyText(d.DenyReason, status)
	WriteErrorPage(w, status, title, text, ev.Hostname)
	ev.Status = status
	in.emit(*ev)
	in.toAgent(peer, *ev)
}

func policyDenyText(reason string, status int) (title, text string) {
	switch reason {
	case model.DenyRateLimit:
		return "Too many requests", "You have sent too many requests. Please slow down and try again shortly."
	case model.DenyGeo:
		return "Blocked", "Access from your location is not allowed for this site."
	default:
		return "Blocked", "Your request was blocked by this site's access policy."
	}
}

// toAgent forwards an event to the owning agent when one is connected.
func (in *Ingress) toAgent(peer registry.Peer, ev model.AccessEvent) {
	if peer != nil && in.opts.OnAgentEvent != nil {
		in.opts.OnAgentEvent(peer, ev)
	}
}

func (in *Ingress) deny(w http.ResponseWriter, ev *model.AccessEvent, status int, reason, title, text string) {
	WriteErrorPage(w, status, title, text, ev.Hostname)
	ev.Status, ev.Decision, ev.DenyReason = status, model.DecisionDeny, reason
	ev.DurationMs = time.Since(ev.TS).Milliseconds()
	in.emit(*ev)
}

func (in *Ingress) emit(ev model.AccessEvent) {
	if in.opts.OnEvent != nil {
		in.opts.OnEvent(ev)
	}
}

func (in *Ingress) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	rec, _ := w.(*recorder)
	host := hostOnly(r.Host)
	var se *session.StreamError
	switch {
	case errors.As(err, &se):
		if rec != nil {
			rec.denyReason = model.DenyUpstream
		}
		WriteErrorPage(w, http.StatusBadGateway, "Target unreachable",
			"The agent is connected but could not reach the local target ("+se.Status.String()+"). Check that the service is running.", host)
	case errors.Is(err, session.ErrTooManyStreams):
		if rec != nil {
			rec.denyReason = model.DenyUpstream
		}
		w.Header().Set("Retry-After", "1")
		WriteErrorPage(w, http.StatusServiceUnavailable, "Busy", "Too many concurrent connections for this tunnel.", host)
	case errors.Is(err, context.Canceled):
		// Client went away; nothing to write.
		if rec != nil {
			rec.status = 499
		}
	default:
		if rec != nil {
			rec.denyReason = model.DenyUpstream
		}
		in.log.Debug("proxy error", "host", host, "err", err)
		WriteErrorPage(w, http.StatusBadGateway, "Tunnel error", "The request could not be forwarded through the tunnel.", host)
	}
}

// RedirectHandler sends plain-HTTP requests to HTTPS. Phase 4 mounts the
// ACME HTTP-01 handler in front of it.
func (in *Ingress) RedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		if host == "" {
			WriteErrorPage(w, http.StatusBadRequest, "Bad request", "Missing Host header.", "")
			return
		}
		target := "https://" + host
		if in.opts.HTTPSPort != 443 {
			target += ":" + strconv.Itoa(in.opts.HTTPSPort)
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

// NewHTTPServer returns an http.Server with the relay's limits applied.
func NewHTTPServer(h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		IdleTimeout:       DefaultIdleTimeout,
		MaxHeaderBytes:    DefaultMaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
}

// recorder captures the status and byte count written by the proxy. Unwrap
// lets http.ResponseController reach Flush/Hijack for WebSocket upgrades.
type recorder struct {
	http.ResponseWriter
	status     int
	bytes      int64
	denyReason string
	wrote      bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// --- TCP tunnels ---

// OnTCPChange is the registry callback: it starts a listener when a port
// gains an entry and stops it when the port is released.
func (in *Ingress) OnTCPChange(port int, e *registry.Entry) {
	in.tcpMu.Lock()
	defer in.tcpMu.Unlock()
	l, have := in.tcp[port]
	switch {
	case e == nil && have:
		_ = l.Close()
		delete(in.tcp, port)
		in.log.Info("tcp tunnel closed", "port", port)
	case e != nil && !have:
		ln, err := net.Listen("tcp", net.JoinHostPort(in.opts.TCPBindHost, strconv.Itoa(port)))
		if err != nil {
			in.log.Error("tcp tunnel listen failed", "port", port, "err", err)
			return
		}
		in.tcp[port] = ln
		in.log.Info("tcp tunnel open", "port", port, "route", e.RouteID)
		in.wg.Add(1)
		go in.acceptTCP(port, ln)
	}
}

// TCPListenerAddr returns the bound address of a TCP tunnel port (tests).
func (in *Ingress) TCPListenerAddr(port int) net.Addr {
	in.tcpMu.Lock()
	defer in.tcpMu.Unlock()
	if l, ok := in.tcp[port]; ok {
		return l.Addr()
	}
	return nil
}

func (in *Ingress) acceptTCP(port int, ln net.Listener) {
	defer in.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go in.handleTCP(port, conn)
	}
}

func (in *Ingress) handleTCP(port int, conn net.Conn) {
	defer conn.Close()
	start := time.Now()
	clientIP, clientPort := splitRemote(conn.RemoteAddr().String())
	ev := model.AccessEvent{TS: start.UTC(), Source: model.SourceRelay, ClientIP: clientIP, Where: "relay",
		Hostname: "tcp:" + strconv.Itoa(port), Decision: model.DecisionAllow}
	e := in.reg.LookupTCP(port)
	if e == nil || !e.Online() {
		ev.Decision, ev.DenyReason = model.DecisionDeny, model.DenyAgentOffline
		ev.DurationMs = time.Since(start).Milliseconds()
		in.emit(ev)
		return
	}
	ev.RouteID = e.RouteID
	hdr := &tunnel.StreamHeader{RouteID: e.RouteID, Kind: model.KindTCP, ClientIP: clientIP, ClientPort: clientPort, StreamID: newStreamID()}
	stream, err := e.Peer.OpenStream(context.Background(), hdr)
	if err != nil {
		ev.Decision, ev.DenyReason = model.DecisionDeny, model.DenyUpstream
		ev.DurationMs = time.Since(start).Milliseconds()
		in.emit(ev)
		return
	}
	in_, out := Pipe(conn, stream)
	ev.BytesIn, ev.BytesOut = in_, out
	ev.DurationMs = time.Since(start).Milliseconds()
	in.emit(ev)
}

// Close stops all TCP listeners.
func (in *Ingress) Close() {
	in.tcpMu.Lock()
	for port, l := range in.tcp {
		_ = l.Close()
		delete(in.tcp, port)
	}
	in.tcpMu.Unlock()
	in.wg.Wait()
}

// Pipe copies bytes in both directions until both sides are done and returns
// the byte counts (client→tunnel, tunnel→client). Each direction half-closes
// its destination when its source hits EOF.
func Pipe(client, tunnelConn net.Conn) (int64, int64) {
	var wg sync.WaitGroup
	var toTunnel, toClient int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		toTunnel, _ = io.Copy(tunnelConn, client)
		closeWrite(tunnelConn)
	}()
	go func() {
		defer wg.Done()
		toClient, _ = io.Copy(client, tunnelConn)
		closeWrite(client)
	}()
	wg.Wait()
	_ = client.Close()
	_ = tunnelConn.Close()
	return toTunnel, toClient
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close() // yamux streams: Close is a half-close for writing
}

// --- connection limits ---

// LimitListener caps total and per-IP concurrent connections; connections
// over the limit are closed immediately.
func LimitListener(l net.Listener, maxTotal, maxPerIP int) net.Listener {
	if maxTotal <= 0 {
		maxTotal = DefaultMaxConns
	}
	if maxPerIP <= 0 {
		maxPerIP = DefaultMaxConnsPerIP
	}
	return &limitListener{Listener: l, sem: make(chan struct{}, maxTotal), perIP: map[string]int{}, maxPerIP: maxPerIP}
}

type limitListener struct {
	net.Listener
	sem      chan struct{}
	mu       sync.Mutex
	perIP    map[string]int
	maxPerIP int
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ip, _ := splitRemote(c.RemoteAddr().String())
		select {
		case l.sem <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		l.mu.Lock()
		if l.perIP[ip] >= l.maxPerIP {
			l.mu.Unlock()
			<-l.sem
			_ = c.Close()
			continue
		}
		l.perIP[ip]++
		l.mu.Unlock()
		return &limitedConn{Conn: c, release: func() {
			l.mu.Lock()
			l.perIP[ip]--
			if l.perIP[ip] == 0 {
				delete(l.perIP, ip)
			}
			l.mu.Unlock()
			<-l.sem
		}}, nil
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// --- helpers ---

// WriteErrorPage writes the relay's HTML error page.
func WriteErrorPage(w http.ResponseWriter, status int, title, text, host string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Latch-Relay", "1")
	w.WriteHeader(status)
	fmt.Fprintf(w, errorPage, status, html.EscapeString(title), status, html.EscapeString(title),
		html.EscapeString(text), html.EscapeString(host))
}

const errorPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%d %s</title>
<style>body{font-family:system-ui,sans-serif;background:#f6f7f9;color:#1f2933;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
main{background:#fff;border-radius:12px;padding:32px 40px;max-width:520px;box-shadow:0 2px 12px rgba(0,0,0,.06)}
h1{font-size:20px;margin:0 0 8px}p{margin:0 0 12px;line-height:1.5}code{background:#eef1f4;padding:2px 6px;border-radius:4px}
small{color:#6b7280}</style></head>
<body><main><h1>%d · %s</h1><p>%s</p><p><code>%s</code></p><small>Served by a Latch relay</small></main></body></html>
`

func hostOnly(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.TrimSuffix(host, ".")
	}
	return strings.TrimSuffix(h, ".")
}

func splitRemote(addr string) (string, int) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

func newStreamID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
