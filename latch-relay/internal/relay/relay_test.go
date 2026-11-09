package relay_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/kayaramazan/latch/latch-relay/internal/devca"
	"github.com/kayaramazan/latch/latch-relay/internal/ingress"
	"github.com/kayaramazan/latch/latch-relay/internal/relay"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

const relayHost = "relay.test"

type harness struct {
	t      *testing.T
	r      *relay.Relay
	pool   *x509.CertPool
	https  string
	http   string
	agent  string
	mu     sync.Mutex
	events []model.AccessEvent
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	files, err := devca.Init(dir+"/devca", []string{relayHost, "*." + relayHost, "127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := devca.LoadServerCert(files)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := devca.CAPool(files.CACert)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, pool: pool}
	lvl := slog.LevelWarn
	if os.Getenv("LATCH_TEST_DEBUG") != "" {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	h.r, err = relay.New(relay.Config{
		DataDir: dir, PublicHost: relayHost, HTTPSAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0", AgentAddr: "127.0.0.1:0",
		TCPBindHost: "127.0.0.1", TCPPortRange: [2]int{41000, 41003}, Certs: relay.StaticCert(cert), Version: "test",
		Logger: log, RevocationCheck: 50 * time.Millisecond,
		OnEvent: func(e model.AccessEvent) {
			h.mu.Lock()
			h.events = append(h.events, e)
			h.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, b, c := h.r.Addrs()
	h.https, h.http, h.agent = a.String(), b.String(), c.String()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.r.Stop(ctx)
	})
	return h
}

func (h *harness) token(name string, hosts []string, tcp bool) string {
	h.t.Helper()
	plain, _, err := h.r.Auth().Create(context.Background(), name, tunnel.Scope{Hostnames: hosts, TCP: tcp}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return plain
}

// client returns an HTTPS client that sends every request to the relay
// regardless of the URL host (the Host header still routes).
func (h *harness) client() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{RootCAs: h.pool},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, h.https)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (h *harness) lastEvent() model.AccessEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.events) == 0 {
		return model.AccessEvent{}
	}
	return h.events[len(h.events)-1]
}

// fakeAgent is a minimal protocol-conformant agent for tests.
type fakeAgent struct {
	t       *testing.T
	conn    *tls.Conn
	mux     *yamux.Session
	ctrl    *tunnel.Codec
	targets map[string]string // routeID → local addr ("" = refuse)
	inbox   chan *tunnel.Envelope
	ack     tunnel.HelloAck
	closed  chan struct{}
	evCh    chan model.AccessEvent
	dials   atomic.Int64
}

func (h *harness) connectAgent(token, agentID string, targets map[string]string) (*fakeAgent, *tunnel.Envelope) {
	h.t.Helper()
	conn, err := tls.Dial("tcp", h.agent, &tls.Config{RootCAs: h.pool, ServerName: relayHost, NextProtos: []string{tunnel.ALPN}})
	if err != nil {
		h.t.Fatal(err)
	}
	mux, err := yamux.Client(conn, tunnel.YamuxConfig())
	if err != nil {
		h.t.Fatal(err)
	}
	ctrlStream, err := mux.OpenStream()
	if err != nil {
		h.t.Fatal(err)
	}
	a := &fakeAgent{t: h.t, conn: conn, mux: mux, ctrl: tunnel.NewCodec(ctrlStream), targets: targets,
		inbox: make(chan *tunnel.Envelope, 64), closed: make(chan struct{}), evCh: make(chan model.AccessEvent, 256)}
	h.t.Cleanup(a.close)
	if _, err := a.ctrl.Send(tunnel.TypeHello, 0, &tunnel.Hello{ProtoMin: 1, ProtoMax: 1, AgentVersion: "test", AgentID: agentID, Token: token, Capabilities: []string{tunnel.CapTCP}}); err != nil {
		h.t.Fatal(err)
	}
	env, err := a.ctrl.Recv()
	if err != nil {
		h.t.Fatal(err)
	}
	if env.T != tunnel.TypeHelloAck {
		return a, env
	}
	if err := env.Decode(&a.ack); err != nil {
		h.t.Fatal(err)
	}
	go a.readLoop()
	go a.acceptLoop()
	return a, env
}

func (a *fakeAgent) close() {
	select {
	case <-a.closed:
	default:
		close(a.closed)
		_ = a.mux.Close()
		_ = a.conn.Close()
	}
}

func (a *fakeAgent) readLoop() {
	for {
		env, err := a.ctrl.Recv()
		if err != nil {
			close(a.inbox)
			return
		}
		if env.T == tunnel.TypePing {
			var p tunnel.Ping
			_ = env.Decode(&p)
			_, _ = a.ctrl.Send(tunnel.TypePong, env.ID, &tunnel.Pong{TS: p.TS})
			continue
		}
		if env.T == tunnel.TypeAccessEvent {
			var batch tunnel.AccessEventBatch
			if env.Decode(&batch) == nil {
				for _, ev := range batch.Events {
					select {
					case a.evCh <- ev:
					default:
					}
				}
			}
			continue
		}
		a.inbox <- env
	}
}

func (a *fakeAgent) acceptLoop() {
	for {
		st, err := a.mux.AcceptStream()
		if err != nil {
			return
		}
		go func() {
			hdr, err := tunnel.ReadStreamHeader(st)
			if err != nil {
				_ = st.Close()
				return
			}
			target := a.targets[hdr.RouteID]
			if target == "" {
				_ = tunnel.WriteStatus(st, tunnel.StatusDialFailed)
				_ = st.Close()
				return
			}
			a.dials.Add(1)
			local, err := net.Dial("tcp", target)
			if err != nil {
				_ = tunnel.WriteStatus(st, tunnel.StatusDialFailed)
				_ = st.Close()
				return
			}
			_ = tunnel.WriteStatus(st, tunnel.StatusOK)
			ingress.Pipe(local, st)
		}()
	}
}

func (a *fakeAgent) announce(specs ...tunnel.RouteSpec) tunnel.RouteAck {
	a.t.Helper()
	id, err := a.ctrl.Send(tunnel.TypeRouteAnnounce, 0, &tunnel.RouteAnnounce{Routes: specs})
	if err != nil {
		a.t.Fatal(err)
	}
	env := a.wait(func(e *tunnel.Envelope) bool { return e.Re == id })
	if env.T != tunnel.TypeRouteAck {
		a.t.Fatalf("expected RouteAck, got %s %s", env.T, env.D)
	}
	var ack tunnel.RouteAck
	_ = env.Decode(&ack)
	return ack
}

func (a *fakeAgent) syncPolicy(ps tunnel.PolicySync) tunnel.PolicyAck {
	a.t.Helper()
	id, err := a.ctrl.Send(tunnel.TypePolicySync, 0, &ps)
	if err != nil {
		a.t.Fatal(err)
	}
	env := a.wait(func(e *tunnel.Envelope) bool { return e.Re == id })
	if env.T != tunnel.TypePolicyAck {
		a.t.Fatalf("expected PolicyAck, got %s %s", env.T, env.D)
	}
	var ack tunnel.PolicyAck
	_ = env.Decode(&ack)
	return ack
}

func (a *fakeAgent) waitEvent(match func(model.AccessEvent) bool) model.AccessEvent {
	a.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-a.evCh:
			if match(ev) {
				return ev
			}
		case <-deadline:
			a.t.Fatal("timeout waiting for access event")
		}
	}
}

func (a *fakeAgent) wait(match func(*tunnel.Envelope) bool) *tunnel.Envelope {
	a.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case env, ok := <-a.inbox:
			if !ok {
				a.t.Fatal("agent control stream closed while waiting")
			}
			if match(env) {
				return env
			}
		case <-deadline:
			a.t.Fatal("timeout waiting for control message")
		}
	}
}

func echoServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = buf.Flush()
			_, _ = io.Copy(conn, buf.Reader)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-XFF", r.Header.Get("X-Forwarded-For"))
		w.Header().Set("X-Seen-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.WriteHeader(200)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func httpSpec(id, host string) tunnel.RouteSpec {
	return tunnel.RouteSpec{RouteID: id, Kind: model.KindHTTP, Hostnames: []string{host}}
}

func TestHTTPEndToEnd(t *testing.T) {
	h := newHarness(t)
	srv := echoServer(t)
	tok := h.token("laptop", []string{"*." + relayHost}, false)
	a, _ := h.connectAgent(tok, "ag_1", map[string]string{"rt_app": srv.Listener.Addr().String()})
	if a.ack.Proto != 1 || a.ack.BaseDomain != relayHost || a.ack.RelayVersion != "test" {
		t.Errorf("hello ack = %+v", a.ack)
	}
	ack := a.announce(httpSpec("rt_app", "app."+relayHost))
	if len(ack.Accepted) != 1 || ack.Accepted[0].PublicURLs[0] != "https://app.relay.test:"+strings.TrimPrefix(h.https, "127.0.0.1:")+"/" {
		t.Fatalf("ack = %+v", ack)
	}

	c := h.client()
	resp, err := c.Post("https://app.relay.test/hello?x=1", "text/plain", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "POST /hello?x=1 body" {
		t.Errorf("status %d body %q", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Seen-Host") != "app.relay.test" || resp.Header.Get("X-Seen-XFF") != "127.0.0.1" || resp.Header.Get("X-Seen-Proto") != "https" {
		t.Errorf("forwarded headers: %v", resp.Header)
	}
	if resp.ProtoMajor != 2 {
		t.Errorf("expected HTTP/2 on the public side, got %s", resp.Proto)
	}
	ev := h.lastEvent()
	if ev.Status != 200 || ev.RouteID != "rt_app" || ev.Hostname != "app.relay.test" || ev.Decision != model.DecisionAllow || ev.Where != "relay" {
		t.Errorf("event = %+v", ev)
	}

	// Unknown host → 404 with relay marker.
	resp, _ = c.Get("https://nope.relay.test/")
	if resp.StatusCode != 404 || resp.Header.Get("X-Latch-Relay") != "1" {
		t.Errorf("unknown host: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if ev := h.lastEvent(); ev.DenyReason != model.DenyUnknownHost {
		t.Errorf("event = %+v", ev)
	}

	// Agent refuses the dial → 502.
	a.targets["rt_app"] = ""
	resp, _ = c.Get("https://app.relay.test/")
	if resp.StatusCode != 502 {
		t.Errorf("dial failed: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if ev := h.lastEvent(); ev.DenyReason != model.DenyUpstream || ev.Status != 502 {
		t.Errorf("event = %+v", ev)
	}

	// Agent disconnects → 503 while the route stays known.
	a.close()
	waitFor(t, func() bool {
		resp, err := c.Get("https://app.relay.test/")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 503 && resp.Header.Get("Retry-After") == "10"
	})
	if ev := h.lastEvent(); ev.DenyReason != model.DenyAgentOffline {
		t.Errorf("event = %+v", ev)
	}
	if e := h.r.Registry().LookupHTTP("app.relay.test", "/"); e == nil || e.Online() {
		t.Errorf("route after disconnect = %+v", e)
	}
}

func TestWebSocketPassthrough(t *testing.T) {
	h := newHarness(t)
	srv := echoServer(t)
	tok := h.token("laptop", []string{"ws." + relayHost}, false)
	a, _ := h.connectAgent(tok, "ag_1", map[string]string{"rt_ws": srv.Listener.Addr().String()})
	a.announce(httpSpec("rt_ws", "ws."+relayHost))

	conn, err := tls.Dial("tcp", h.https, &tls.Config{RootCAs: h.pool, ServerName: "ws.relay.test", NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET /chat HTTP/1.1\r\nHost: ws.relay.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 101 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, msg := range []string{"hello", "world"} {
		fmt.Fprint(conn, msg)
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(br, buf); err != nil || string(buf) != msg {
			t.Fatalf("echo %q: got %q, %v", msg, buf, err)
		}
	}
}

func TestTCPTunnel(t *testing.T) {
	h := newHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	tok := h.token("laptop", nil, true)
	a, _ := h.connectAgent(tok, "ag_1", map[string]string{"rt_tcp": ln.Addr().String()})
	if !a.ack.Scope.TCP || len(a.ack.Scope.TCPPortRange) != 2 {
		t.Errorf("scope = %+v", a.ack.Scope)
	}
	ack := a.announce(tunnel.RouteSpec{RouteID: "rt_tcp", Kind: model.KindTCP})
	if len(ack.Accepted) != 1 || ack.Accepted[0].TCPPort != 41000 || ack.Accepted[0].PublicURLs[0] != "tcp://relay.test:41000" {
		t.Fatalf("ack = %+v", ack)
	}
	c, err := net.DialTimeout("tcp", "127.0.0.1:41000", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(c, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo: %q %v", buf, err)
	}
	c.Close()
	waitFor(t, func() bool { return h.lastEvent().RouteID == "rt_tcp" && h.lastEvent().BytesIn == 4 })

	// Withdraw closes the listener.
	a.announce()
	waitFor(t, func() bool {
		c, err := net.DialTimeout("tcp", "127.0.0.1:41000", 200*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err != nil
	})
}

func TestAuthScopeAndConflicts(t *testing.T) {
	h := newHarness(t)
	_, env := h.connectAgent("lt_bogus_token", "ag_x", nil)
	var e tunnel.Error
	if env.T != tunnel.TypeError || env.Decode(&e) != nil || e.Code != tunnel.ErrAuth || !e.Fatal {
		t.Errorf("bad token: %s %s", env.T, env.D)
	}

	tokA := h.token("a", []string{"*." + relayHost}, false)
	tokB := h.token("b", []string{"*." + relayHost}, false)
	a, _ := h.connectAgent(tokA, "ag_a", nil)
	ack := a.announce(httpSpec("rt_1", "app."+relayHost), httpSpec("rt_2", "other.example.com"))
	if len(ack.Accepted) != 1 || len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrScope {
		t.Errorf("scope: %+v", ack)
	}
	b, _ := h.connectAgent(tokB, "ag_b", nil)
	ack = b.announce(httpSpec("rt_9", "app."+relayHost))
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrHostnameTaken {
		t.Errorf("taken: %+v", ack)
	}

	// Unknown message type is answered, not fatal.
	id, _ := a.ctrl.Send(tunnel.Type("Bogus"), 0, nil)
	env = a.wait(func(e *tunnel.Envelope) bool { return e.Re == id })
	if env.T != tunnel.TypeError || env.Decode(&e) != nil || e.Code != tunnel.ErrUnknownType || e.Fatal {
		t.Errorf("unknown type: %s %s", env.T, env.D)
	}
	// PolicySync is acknowledged with the installed version (phase 5).
	id, _ = a.ctrl.Send(tunnel.TypePolicySync, 0, &tunnel.PolicySync{Version: 3})
	env = a.wait(func(e *tunnel.Envelope) bool { return e.Re == id })
	var pack tunnel.PolicyAck
	if env.T != tunnel.TypePolicyAck || env.Decode(&pack) != nil || pack.Version != 3 {
		t.Errorf("policy sync: %s %s", env.T, env.D)
	}
}

func TestSupersedeAndRevoke(t *testing.T) {
	h := newHarness(t)
	srv := echoServer(t)
	tok := h.token("laptop", []string{"*." + relayHost}, false)
	first, _ := h.connectAgent(tok, "ag_same", map[string]string{"rt_app": srv.Listener.Addr().String()})
	first.announce(httpSpec("rt_app", "app."+relayHost))

	second, _ := h.connectAgent(tok, "ag_same", map[string]string{"rt_app": srv.Listener.Addr().String()})
	env := first.wait(func(e *tunnel.Envelope) bool { return e.T == tunnel.TypeGoodbye })
	var bye tunnel.Goodbye
	if env.Decode(&bye) != nil || bye.Reason != tunnel.ByeSuperseded {
		t.Errorf("goodbye = %+v", bye)
	}
	second.announce(httpSpec("rt_app", "app."+relayHost))
	resp, err := h.client().Get("https://app.relay.test/")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("via second session: %v %v", resp, err)
	}
	resp.Body.Close()
	if n := len(h.r.Sessions().Sessions()); n != 1 {
		t.Errorf("sessions = %d", n)
	}

	// Revoke through the store only (as the CLI does); the watcher closes it.
	toks, _ := h.r.Auth().List(context.Background())
	if err := h.r.Auth().Revoke(context.Background(), toks[0].ID); err != nil {
		t.Fatal(err)
	}
	env = second.wait(func(e *tunnel.Envelope) bool { return e.T == tunnel.TypeGoodbye })
	if env.Decode(&bye) != nil || bye.Reason != tunnel.ByeTokenRevoked {
		t.Errorf("goodbye = %+v", bye)
	}
	waitFor(t, func() bool {
		resp, err := h.client().Get("https://app.relay.test/")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 404
	})
	if _, env := h.connectAgent(tok, "ag_same", nil); env.T != tunnel.TypeError {
		t.Error("revoked token could connect")
	}
}

func TestHTTPRedirectAndALPN(t *testing.T) {
	h := newHarness(t)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).
		Get("http://" + h.http + "/x?y=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	_, port, _ := net.SplitHostPort(h.https)
	if resp.StatusCode != 308 || resp.Header.Get("Location") != "https://127.0.0.1:"+port+"/x?y=1" {
		t.Errorf("redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// A TLS client without the latch ALPN is dropped by the agent listener.
	conn, err := tls.Dial("tcp", h.agent, &tls.Config{RootCAs: h.pool, ServerName: relayHost})
	if err == nil {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Error("connection without ALPN stayed open")
		}
		conn.Close()
	}
}

func TestPersistedRoutesSurviveRestart(t *testing.T) {
	h := newHarness(t)
	tok := h.token("laptop", []string{"*." + relayHost}, false)
	a, _ := h.connectAgent(tok, "ag_1", nil)
	a.announce(httpSpec("rt_app", "app."+relayHost))
	a.close()
	ctx := context.Background()
	if err := h.r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// Reopen on the same data dir: the hostname is still owned and offline.
	r2, err := relay.New(relay.Config{DataDir: h.r.DataDir(), PublicHost: relayHost, HTTPSAddr: "127.0.0.1:0", HTTPAddr: "",
		AgentAddr: "127.0.0.1:0", Certs: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := r2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r2.Stop(ctx)
	if e := r2.Registry().LookupHTTP("app.relay.test", "/"); e == nil || e.Online() || e.RouteID != "rt_app" {
		t.Errorf("after restart: %+v", e)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestPolicyIPDeny(t *testing.T) {
	h := newHarness(t)
	srv := echoServer(t)
	tok := h.token("laptop", []string{"*." + relayHost}, false)
	a, _ := h.connectAgent(tok, "ag_1", map[string]string{"rt_app": srv.Listener.Addr().String()})
	a.announce(httpSpec("rt_app", "app."+relayHost))
	a.syncPolicy(tunnel.PolicySync{
		Version: 1,
		IPPolicies: []model.IPPolicy{{ID: "pol_block", Rules: []model.IPRule{
			{Action: model.ActionDeny, CIDR: "127.0.0.0/8"}}}},
		Bindings: []tunnel.PolicyBinding{{RouteID: "rt_app", IPPolicyID: "pol_block"}},
	})

	dialsBefore := a.dials.Load()
	c := h.client()
	resp, err := c.Get("https://app.relay.test/secret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Latch-Relay") != "1" {
		t.Error("deny page should carry the relay marker")
	}
	if got := a.dials.Load(); got != dialsBefore {
		t.Errorf("denied request must not reach the agent (dials %d -> %d)", dialsBefore, got)
	}
	ev := a.waitEvent(func(ev model.AccessEvent) bool { return ev.Decision == model.DecisionDeny })
	if ev.DenyReason != model.DenyIPPolicy || ev.RuleID != "pol_block" || ev.Where != "relay" {
		t.Errorf("event = %+v", ev)
	}
}

func TestPolicyRateLimit(t *testing.T) {
	h := newHarness(t)
	srv := echoServer(t)
	tok := h.token("laptop", []string{"*." + relayHost}, false)
	a, _ := h.connectAgent(tok, "ag_1", map[string]string{"rt_app": srv.Listener.Addr().String()})
	a.announce(httpSpec("rt_app", "app."+relayHost))
	a.syncPolicy(tunnel.PolicySync{
		Version:    1,
		RateLimits: []model.RateLimitRule{{ID: "rl1", Keys: []string{"ip"}, Limit: 2, WindowSec: 60}},
		Bindings:   []tunnel.PolicyBinding{{RouteID: "rt_app", RateLimitIDs: []string{"rl1"}}},
	})

	c := h.client()
	for i := 0; i < 2; i++ {
		resp, err := c.Get("https://app.relay.test/")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}
	resp, err := c.Get("https://app.relay.test/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 should include Retry-After")
	}
	ev := a.waitEvent(func(ev model.AccessEvent) bool { return ev.DenyReason == model.DenyRateLimit })
	if ev.Status != http.StatusTooManyRequests {
		t.Errorf("rate-limit event = %+v", ev)
	}
}
