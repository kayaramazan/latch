package policies

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func denyPolicy() *tunnel.PolicySync {
	return &tunnel.PolicySync{
		Version: 1,
		IPPolicies: []model.IPPolicy{{ID: "pol_block", Rules: []model.IPRule{
			{Action: model.ActionDeny, CIDR: "203.0.113.0/24"}}}},
		Bindings: []tunnel.PolicyBinding{{RouteID: "rt_app", IPPolicyID: "pol_block"}},
	}
}

func req(ip string) policy.Request { return policy.Request{RemoteAddr: ip + ":1000"} }

func TestSyncAndEvaluate(t *testing.T) {
	st := openStore(t)
	m, _ := New(Options{Store: st})
	ctx := context.Background()
	if _, _, err := m.Sync(ctx, "tok1", denyPolicy()); err != nil {
		t.Fatal(err)
	}
	if m.Version("tok1") != 1 {
		t.Errorf("version = %d", m.Version("tok1"))
	}
	if d := m.Evaluate("tok1", "rt_app", req("203.0.113.5"), time.Now()); d.Allow {
		t.Error("203.0.113.5 should be denied")
	}
	if d := m.Evaluate("tok1", "rt_app", req("8.8.8.8"), time.Now()); !d.Allow {
		t.Error("8.8.8.8 should be allowed")
	}
	// Unknown token: allow (no policy).
	if d := m.Evaluate("other", "rt_app", req("203.0.113.5"), time.Now()); !d.Allow {
		t.Error("unknown token should allow")
	}
}

func TestPersistenceSurvivesRestart(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	m1, _ := New(Options{Store: st})
	if _, _, err := m1.Sync(ctx, "tok1", denyPolicy()); err != nil {
		t.Fatal(err)
	}
	// A fresh manager (relay restart) loads the policy from the store and
	// enforces it even before any agent reconnects.
	m2, _ := New(Options{Store: st})
	if err := m2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if d := m2.Evaluate("tok1", "rt_app", req("203.0.113.5"), time.Now()); d.Allow {
		t.Error("persisted policy should deny after restart")
	}
	// Drop removes it.
	m2.Drop(ctx, "tok1")
	if d := m2.Evaluate("tok1", "rt_app", req("203.0.113.5"), time.Now()); !d.Allow {
		t.Error("dropped policy should no longer deny")
	}
}

func TestHeaderApplication(t *testing.T) {
	st := openStore(t)
	m, _ := New(Options{Store: st})
	ps := &tunnel.PolicySync{Version: 1, Bindings: []tunnel.PolicyBinding{{RouteID: "rt_app",
		Headers: []model.HeaderRule{{Phase: model.HeaderRequest, Op: model.HeaderSet, Name: "X-From", Value: "latch"}}}}}
	if _, _, err := m.Sync(context.Background(), "tok1", ps); err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	m.ApplyRequest("tok1", "rt_app", h)
	if h.Get("X-From") != "latch" {
		t.Errorf("header not applied: %v", h)
	}
}
