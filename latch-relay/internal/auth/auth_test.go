package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/proto/tunnel"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st)
}

func TestCreateVerifyRevoke(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	scope := tunnel.Scope{Hostnames: []string{"*.relay.example.com"}, TCP: true}
	plain, tok, err := m.Create(ctx, "laptop", scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "lt_"+tok.ID+"_") {
		t.Errorf("token format: %s", plain)
	}
	secret := plain[len("lt_")+len(tok.ID)+1:]
	if strings.Contains(tok.Hash, secret) || !strings.HasPrefix(tok.Hash, "$argon2id$") {
		t.Error("secret stored in clear or wrong hash format")
	}
	got, err := m.Verify(ctx, plain)
	if err != nil || got.ID != tok.ID {
		t.Fatalf("verify: %+v, %v", got, err)
	}
	list, _ := m.List(ctx)
	if len(list) != 1 || list[0].LastSeenAt == nil {
		t.Errorf("list after verify: %+v", list)
	}

	for _, bad := range []string{"", "lt_", "lt_" + tok.ID, "lt_" + tok.ID + "_wrong", "xx_" + tok.ID + "_s", "lt_unknown_secret", plain + "x"} {
		if _, err := m.Verify(ctx, bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	if err := m.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, plain); !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
	if err := m.Revoke(ctx, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double revoke: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	plain, _, err := m.Create(ctx, "temp", tunnel.Scope{Hostnames: []string{"a.example.com"}}, &exp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, plain); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := m.Verify(ctx, plain); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	if _, _, err := m.Create(ctx, "", tunnel.Scope{Hostnames: []string{"a.example.com"}}, nil); err == nil {
		t.Error("empty name accepted")
	}
	if _, _, err := m.Create(ctx, "x", tunnel.Scope{}, nil); err == nil {
		t.Error("empty scope accepted")
	}
	for _, bad := range []string{"*.*.example.com", "a.*.example.com", "example", "*.example"} {
		if _, _, err := m.Create(ctx, "x", tunnel.Scope{Hostnames: []string{bad}}, nil); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, _, err := m.Create(ctx, "x", tunnel.Scope{TCP: true, TCPPortRange: []int{5, 1}}, nil); err == nil {
		t.Error("bad port range accepted")
	}
	// Uppercase input is normalized rather than rejected.
	_, tok, err := m.Create(ctx, "x", tunnel.Scope{Hostnames: []string{"UPPER.example.com"}}, nil)
	if err != nil || tok.Scope.Hostnames[0] != "upper.example.com" {
		t.Errorf("normalize on create: %+v %v", tok, err)
	}
}

func TestScopeAllows(t *testing.T) {
	s := NormalizeScope(tunnel.Scope{Hostnames: []string{" App.Example.ORG ", "*.relay.example.com", "*.relay.example.com"}})
	if len(s.Hostnames) != 2 {
		t.Errorf("normalize: %v", s.Hostnames)
	}
	cases := map[string]bool{
		"app.example.org":          true,
		"APP.example.org":          true,
		"other.example.org":        false,
		"a.relay.example.com":      true,
		"a.b.relay.example.com":    false,
		"relay.example.com":        false,
		"xrelay.example.com":       false,
		"a.relay.example.com.more": false,
	}
	for h, want := range cases {
		if got := ScopeAllows(s, h); got != want {
			t.Errorf("%s: got %v want %v", h, got, want)
		}
	}
}

// TestTokenEncodingHasNoDelimiter guards against delimiter collisions: the id
// and secret encodings must never contain '_', or split() would mis-parse a
// "lt_<id>_<secret>" token. This exercises randomString directly (no argon2).
func TestTokenEncodingHasNoDelimiter(t *testing.T) {
	for i := 0; i < 20000; i++ {
		id, secret := randomString(8), randomString(32)
		if strings.ContainsAny(id, "_-=") || strings.ContainsAny(secret, "_-=") {
			t.Fatalf("encoding produced a delimiter char: id=%q secret=%q", id, secret)
		}
		plain := "lt_" + id + "_" + secret
		gotID, gotSecret, ok := split(plain)
		if !ok || gotID != id || gotSecret != secret {
			t.Fatalf("split(%q) = %q,%q,%v", plain, gotID, gotSecret, ok)
		}
	}
}
