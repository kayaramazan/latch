package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCustomDomainLifecycle(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	if st.IsRegistered("app.customer.com") {
		t.Fatal("should not be registered yet")
	}
	if err := st.AddCustomDomain(ctx, "App.Customer.com.", "relay.example.com", "team blue"); err != nil {
		t.Fatal(err)
	}
	if !st.IsRegistered("app.customer.com") {
		t.Fatal("should be registered (normalized)")
	}
	d, err := st.GetCustomDomain(ctx, "app.customer.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Target != "relay.example.com" || d.Note != "team blue" || d.VerifiedAt != nil {
		t.Errorf("domain = %+v", d)
	}

	when := time.Now().UTC()
	if err := st.SetCustomDomainVerified(ctx, "app.customer.com", when); err != nil {
		t.Fatal(err)
	}
	d, _ = st.GetCustomDomain(ctx, "app.customer.com")
	if d.VerifiedAt == nil {
		t.Error("verified_at should be set")
	}

	list, err := st.ListCustomDomains(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}

	if err := st.RemoveCustomDomain(ctx, "app.customer.com"); err != nil {
		t.Fatal(err)
	}
	if st.IsRegistered("app.customer.com") {
		t.Error("should be gone")
	}
	if err := st.RemoveCustomDomain(ctx, "app.customer.com"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
