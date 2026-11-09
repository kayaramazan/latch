package registry

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

type fakePeer struct{ sid, tok, agent string }

func (p fakePeer) SessionID() string { return p.sid }
func (p fakePeer) TokenID() string   { return p.tok }
func (p fakePeer) AgentID() string   { return p.agent }
func (p fakePeer) OpenStream(context.Context, *tunnel.StreamHeader) (net.Conn, error) {
	return nil, nil
}

func newReg(t *testing.T, onTCP func(int, *Entry)) (*Registry, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, id := range []string{"tokA", "tokB"} {
		if err := st.InsertToken(context.Background(), &store.Token{ID: id, Name: id, Hash: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	r := New(st, Options{PublicHost: "relay.example.com", TCPPortRange: [2]int{20000, 20002}, OnTCPChange: onTCP})
	return r, st
}

var scopeA = tunnel.Scope{Hostnames: []string{"*.relay.example.com", "custom.example.org"}, TCP: true}

func TestAnnounceLookupAndFullSet(t *testing.T) {
	r, _ := newReg(t, nil)
	ctx := context.Background()
	p := fakePeer{"s1", "tokA", "ag1"}

	ack := r.Announce(ctx, p, scopeA, []tunnel.RouteSpec{
		{RouteID: "rt_1", Kind: model.KindHTTP, Hostnames: []string{"App.relay.example.com"}},
		{RouteID: "rt_2", Kind: model.KindHTTP, Hostnames: []string{"app.relay.example.com"}, PathPrefix: "/api/"},
		{RouteID: "rt_3", Kind: model.KindHTTP, Hostnames: []string{"nope.example.org"}},
	})
	if len(ack.Accepted) != 2 || len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrScope {
		t.Fatalf("ack = %+v", ack)
	}
	if ack.Accepted[0].PublicURLs[0] != "https://app.relay.example.com/" || ack.Accepted[1].PublicURLs[0] != "https://app.relay.example.com/api/" {
		t.Errorf("urls = %v %v", ack.Accepted[0].PublicURLs, ack.Accepted[1].PublicURLs)
	}

	if e := r.LookupHTTP("app.relay.example.com", "/"); e == nil || e.RouteID != "rt_1" || !e.Online() {
		t.Errorf("lookup / = %+v", e)
	}
	if e := r.LookupHTTP("APP.relay.example.com.", "/api/users"); e == nil || e.RouteID != "rt_2" {
		t.Errorf("lookup /api/users = %+v", e)
	}
	if e := r.LookupHTTP("app.relay.example.com", "/apix"); e == nil || e.RouteID != "rt_1" {
		t.Errorf("lookup /apix should fall back to / = %+v", e)
	}
	if r.LookupHTTP("other.relay.example.com", "/") != nil {
		t.Error("unknown host found")
	}

	// Re-announce without rt_2: it is withdrawn.
	r.Announce(ctx, p, scopeA, []tunnel.RouteSpec{{RouteID: "rt_1", Kind: model.KindHTTP, Hostnames: []string{"app.relay.example.com"}}})
	if e := r.LookupHTTP("app.relay.example.com", "/api/x"); e == nil || e.RouteID != "rt_1" {
		t.Errorf("after withdraw: %+v", e)
	}
	if len(r.Entries()) != 1 {
		t.Errorf("entries = %+v", r.Entries())
	}
}

func TestConflictsAndTakeover(t *testing.T) {
	r, _ := newReg(t, nil)
	ctx := context.Background()
	a := fakePeer{"s1", "tokA", "ag1"}
	b := fakePeer{"s2", "tokB", "ag2"}
	spec := []tunnel.RouteSpec{{RouteID: "rt_x", Kind: model.KindHTTP, Hostnames: []string{"app.relay.example.com"}}}
	r.Announce(ctx, a, scopeA, spec)

	ack := r.Announce(ctx, b, scopeA, spec)
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrHostnameTaken {
		t.Fatalf("other token should be rejected: %+v", ack)
	}
	if e := r.LookupHTTP("app.relay.example.com", "/"); e.TokenID != "tokA" {
		t.Error("ownership changed")
	}

	// Same token, another device: takes over.
	a2 := fakePeer{"s3", "tokA", "ag9"}
	ack = r.Announce(ctx, a2, scopeA, spec)
	if len(ack.Accepted) != 1 {
		t.Fatalf("same token takeover rejected: %+v", ack)
	}
	if e := r.LookupHTTP("app.relay.example.com", "/"); e.AgentID != "ag9" || e.Peer.SessionID() != "s3" {
		t.Errorf("takeover: %+v", e)
	}

	// Detaching the old session must not affect the new owner.
	r.Detach(ctx, a)
	if e := r.LookupHTTP("app.relay.example.com", "/"); !e.Online() {
		t.Error("detach of old session took new session offline")
	}
	r.Detach(ctx, a2)
	if e := r.LookupHTTP("app.relay.example.com", "/"); e == nil || e.Online() {
		t.Errorf("after detach: %+v", e)
	}

	// Duplicate hostname inside one announce.
	ack = r.Announce(ctx, a2, scopeA, []tunnel.RouteSpec{
		{RouteID: "rt_1", Kind: model.KindHTTP, Hostnames: []string{"dup.relay.example.com"}},
		{RouteID: "rt_2", Kind: model.KindHTTP, Hostnames: []string{"dup.relay.example.com"}},
	})
	if len(ack.Rejected) != 1 || ack.Rejected[0].RouteID != "rt_2" {
		t.Errorf("dup in batch: %+v", ack)
	}

	// Invalid specs.
	ack = r.Announce(ctx, a2, scopeA, []tunnel.RouteSpec{
		{RouteID: "", Kind: model.KindHTTP, Hostnames: []string{"a.relay.example.com"}},
		{RouteID: "rt_h", Kind: model.KindHTTP},
		{RouteID: "rt_p", Kind: model.KindHTTP, Hostnames: []string{"a.relay.example.com"}, PathPrefix: "api"},
		{RouteID: "rt_k", Kind: "udp"},
		{RouteID: "rt_bad", Kind: model.KindHTTP, Hostnames: []string{"bad_host.relay.example.com"}},
	})
	if len(ack.Rejected) != 5 {
		t.Errorf("invalid specs: %+v", ack)
	}
}

func TestTCPPortsAndCallbacks(t *testing.T) {
	var mu sync.Mutex
	events := map[int]bool{}
	r, _ := newReg(t, func(port int, e *Entry) {
		mu.Lock()
		defer mu.Unlock()
		events[port] = e != nil
	})
	ctx := context.Background()
	p := fakePeer{"s1", "tokA", "ag1"}

	ack := r.Announce(ctx, p, scopeA, []tunnel.RouteSpec{
		{RouteID: "rt_a", Kind: model.KindTCP},
		{RouteID: "rt_b", Kind: model.KindTCP, TCPPort: 20002},
		{RouteID: "rt_c", Kind: model.KindTCP, TCPPort: 30000},
	})
	if len(ack.Accepted) != 2 || ack.Accepted[0].TCPPort != 20000 || ack.Accepted[1].TCPPort != 20002 {
		t.Fatalf("ack = %+v", ack)
	}
	if ack.Accepted[0].PublicURLs[0] != "tcp://relay.example.com:20000" {
		t.Errorf("url = %v", ack.Accepted[0].PublicURLs)
	}
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrTCPPortUnavailable {
		t.Errorf("out of range: %+v", ack.Rejected)
	}
	mu.Lock()
	if !events[20000] || !events[20002] {
		t.Errorf("callbacks = %v", events)
	}
	mu.Unlock()

	// Reconnect: rt_a keeps port 20000 even though 20001 is lower-free? (20000 is its own.)
	r.Detach(ctx, p)
	ack = r.Announce(ctx, p, scopeA, []tunnel.RouteSpec{{RouteID: "rt_a", Kind: model.KindTCP}})
	if ack.Accepted[0].TCPPort != 20000 {
		t.Errorf("port not stable: %+v", ack)
	}
	mu.Lock()
	if events[20002] {
		t.Error("withdrawn port callback missing")
	}
	mu.Unlock()
	if r.LookupTCP(20002) != nil {
		t.Error("withdrawn port still present")
	}

	// Pool exhaustion and foreign-port rejection.
	other := fakePeer{"s2", "tokB", "ag2"}
	r.Announce(ctx, other, scopeA, []tunnel.RouteSpec{{RouteID: "rt_o1", Kind: model.KindTCP}, {RouteID: "rt_o2", Kind: model.KindTCP}})
	ack = r.Announce(ctx, other, scopeA, []tunnel.RouteSpec{{RouteID: "rt_o1", Kind: model.KindTCP}, {RouteID: "rt_o2", Kind: model.KindTCP}, {RouteID: "rt_o3", Kind: model.KindTCP}})
	if len(ack.Rejected) != 1 || ack.Rejected[0].RouteID != "rt_o3" {
		t.Errorf("exhaustion: %+v", ack)
	}
	ack = r.Announce(ctx, other, scopeA, []tunnel.RouteSpec{{RouteID: "rt_o4", Kind: model.KindTCP, TCPPort: 20000}})
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrTCPPortUnavailable {
		t.Errorf("foreign port: %+v", ack)
	}
	noTCP := tunnel.Scope{Hostnames: []string{"x.example.com"}}
	ack = r.Announce(ctx, other, noTCP, []tunnel.RouteSpec{{RouteID: "rt_o5", Kind: model.KindTCP}})
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != tunnel.ErrTCPNotAllowed {
		t.Errorf("tcp not allowed: %+v", ack)
	}
}

func TestPersistenceAndWithdraw(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_ = st.InsertToken(ctx, &store.Token{ID: "tokA", Name: "a", Hash: "x"})
	r := New(st, Options{PublicHost: "relay.example.com"})
	p := fakePeer{"s1", "tokA", "ag1"}
	r.Announce(ctx, p, scopeA, []tunnel.RouteSpec{
		{RouteID: "rt_1", Kind: model.KindHTTP, Hostnames: []string{"app.relay.example.com"}},
		{RouteID: "rt_2", Kind: model.KindTCP},
	})

	var ports []int
	r2 := New(st, Options{PublicHost: "relay.example.com", OnTCPChange: func(port int, e *Entry) { ports = append(ports, port) }})
	if err := r2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	e := r2.LookupHTTP("app.relay.example.com", "/")
	if e == nil || e.Online() || e.TokenID != "tokA" {
		t.Errorf("loaded entry = %+v", e)
	}
	if r2.LookupTCP(20000) == nil || len(ports) != 1 {
		t.Errorf("tcp loaded = %+v ports=%v", r2.LookupTCP(20000), ports)
	}

	r2.Withdraw(ctx, p, []string{"rt_1", "rt_2"})
	if r2.LookupHTTP("app.relay.example.com", "/") != nil || r2.LookupTCP(20000) != nil {
		t.Error("withdraw left entries")
	}
	hr, _ := st.HTTPRoutes(ctx)
	tr, _ := st.TCPRoutes(ctx)
	if len(hr) != 0 || len(tr) != 0 {
		t.Error("withdraw left rows")
	}

	r2.Announce(ctx, p, scopeA, []tunnel.RouteSpec{{RouteID: "rt_1", Kind: model.KindHTTP, Hostnames: []string{"app.relay.example.com"}}})
	r2.DropToken("tokA")
	if len(r2.Entries()) != 0 {
		t.Error("DropToken left entries")
	}
}
