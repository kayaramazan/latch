package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// testStore runs the conformance suite against any Store implementation.
func testStore(t *testing.T, st Store) {
	ctx := context.Background()
	acct := Account{ID: "acc_1", Name: "Acme", Status: AccountActive, PlanID: "free", Region: "eu", CreatedAt: time.Now().UTC()}
	owner := User{ID: "usr_1", Email: "a@example.com", PasswordHash: "h", Role: RoleOwner, CreatedAt: time.Now().UTC()}
	if _, _, err := st.CreateAccountWithOwner(ctx, acct, owner); err != nil {
		t.Fatal(err)
	}
	// Duplicate email → conflict.
	if _, _, err := st.CreateAccountWithOwner(ctx, Account{ID: "acc_2", Status: AccountActive, PlanID: "free", Region: "eu", CreatedAt: time.Now().UTC()},
		User{ID: "usr_2", Email: "a@example.com", PasswordHash: "h", Role: RoleOwner, CreatedAt: time.Now().UTC()}); err != ErrConflict {
		t.Errorf("expected conflict, got %v", err)
	}
	if u, err := st.GetUserByEmail(ctx, "a@example.com"); err != nil || u.AccountID != "acc_1" {
		t.Fatalf("get user: %v %+v", err, u)
	}

	// A second tenant.
	acct2 := Account{ID: "acc_b", Name: "B", Status: AccountActive, PlanID: "free", Region: "eu", CreatedAt: time.Now().UTC()}
	if _, _, err := st.CreateAccountWithOwner(ctx, acct2, User{ID: "usr_b", Email: "b@example.com", PasswordHash: "h", Role: RoleOwner, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	// Token for A; B cannot see or touch it.
	tok := TunnelToken{ID: "rt_1", AccountID: "acc_1", RelayID: "relay-eu-1", Name: "laptop", Hostnames: []string{"x.eu.latch.app"}, Status: "active", CreatedAt: time.Now().UTC()}
	if err := st.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if ts, _ := st.ListTokens(ctx, "acc_b"); len(ts) != 0 {
		t.Errorf("tenant B must see no tokens, saw %d", len(ts))
	}
	if _, err := st.GetToken(ctx, "acc_b", "rt_1"); err != ErrNotFound {
		t.Errorf("B getting A's token should be NotFound, got %v", err)
	}
	got, err := st.GetToken(ctx, "acc_1", "rt_1")
	if err != nil || len(got.Hostnames) != 1 || got.Hostnames[0] != "x.eu.latch.app" {
		t.Fatalf("A's token: %v %+v", err, got)
	}
	if err := st.SetTokenStatus(ctx, "acc_b", "rt_1", "revoked"); err != ErrNotFound {
		t.Errorf("B revoking A's token should be NotFound, got %v", err)
	}

	// Suspend A.
	if err := st.SetAccountStatus(ctx, "acc_1", AccountSuspended, "abuse"); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.GetAccount(ctx, "acc_1"); a.Status != AccountSuspended || a.SuspendedAt == nil {
		t.Errorf("suspend not applied: %+v", a)
	}

	// Blocklist.
	_ = st.AddBlocklist(ctx, "evil.com", "phishing")
	if b, _ := st.IsBlocked(ctx, "sub.evil.com"); !b {
		t.Error("sub.evil.com should be blocked")
	}
	if b, _ := st.IsBlocked(ctx, "good.com"); b {
		t.Error("good.com should not be blocked")
	}

	// Audit.
	_ = st.AppendAudit(ctx, AuditEntry{ID: "aud_1", AccountID: "acc_1", Actor: "user", Action: "login", CreatedAt: time.Now().UTC()})
	if es, _ := st.ListAudit(ctx, "acc_1", 10); len(es) != 1 {
		t.Errorf("audit: expected 1, got %d", len(es))
	}

	// API key.
	if err := st.CreateAPIKey(ctx, APIKey{ID: "key_1", AccountID: "acc_1", Name: "ci", Hash: "deadbeef", Prefix: "lk_ci", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if k, err := st.GetAPIKeyByHash(ctx, "deadbeef"); err != nil || k.AccountID != "acc_1" {
		t.Fatalf("api key: %v %+v", err, k)
	}
}

func TestMemoryStore(t *testing.T) {
	st := NewMemory()
	defer st.Close()
	testStore(t, st)
}

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("LATCH_CONTROL_DB")
	if dsn == "" {
		t.Skip("set LATCH_CONTROL_DB to run the PostgreSQL conformance test")
	}
	st, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Clean up from any previous run.
	for _, tbl := range []string{"audit_log", "api_keys", "sessions", "tunnel_tokens", "custom_domains", "projects", "users", "accounts", "blocklist"} {
		_, _ = st.db.Exec("DELETE FROM " + tbl)
	}
	testStore(t, st)
}
