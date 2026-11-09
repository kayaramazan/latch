package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kayaramazan/latch/proto/model"
)

func TestDiffAgainstCurrentAndUnknown(t *testing.T) {
	s, _ := open(t, Options{})
	ctx := context.Background()
	addRoute(t, s, "a", "a.example.com")
	d, err := s.Diff(ctx, 1, 0)
	if err != nil || len(d.Added) != 1 {
		t.Errorf("diff 1→current = %+v, %v", d, err)
	}
	if _, err := s.Diff(ctx, 42, 0); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("unknown a: %v", err)
	}
	if _, err := s.Diff(ctx, 0, 42); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("unknown b: %v", err)
	}
	if _, err := s.Rollback(ctx, 42, "t"); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("rollback unknown: %v", err)
	}
	vs, err := s.Versions(ctx, 0, 0)
	if err != nil || len(vs) != 2 {
		t.Errorf("default limit: %d, %v", len(vs), err)
	}
}

func TestImportMergeAllKinds(t *testing.T) {
	s, _ := open(t, Options{})
	ctx := context.Background()
	relay := addRelay(t, s)
	pol := model.NewIPPolicy("p")
	rl := model.NewRateLimitRule("r", 10, 60)
	if _, err := s.Update(ctx, "t", "", func(snap *model.Snapshot) error {
		snap.IPPolicies = append(snap.IPPolicies, pol)
		snap.RateLimits = append(snap.RateLimits, rl)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	in := model.NewSnapshot()
	relay.Name = "vps-renamed"
	pol.Name = "p-renamed"
	rl.Name = "r-renamed"
	in.Relays = []model.Relay{relay, model.NewRelay("second", "r2.example.com", "tok2")}
	in.IPPolicies = []model.IPPolicy{pol, model.NewIPPolicy("p2")}
	in.RateLimits = []model.RateLimitRule{rl, model.NewRateLimitRule("r2", 1, 1)}
	in.Settings.Language = "tr"
	if _, err := s.Import(ctx, in, ImportMerge, "t"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Current()
	if len(got.Relays) != 2 || got.Relay(relay.ID).Name != "vps-renamed" ||
		len(got.IPPolicies) != 2 || got.IPPolicy(pol.ID).Name != "p-renamed" ||
		len(got.RateLimits) != 2 || got.RateLimit(rl.ID).Name != "r-renamed" ||
		got.Settings.Language != "tr" {
		t.Errorf("merge result: %+v", got)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "dir", "x.db"), Options{}); err == nil {
		t.Error("unwritable path accepted")
	}

	path := filepath.Join(t.TempDir(), "corrupt.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE config_versions SET snapshot = X'7B' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path, Options{}); err == nil {
		t.Error("corrupt snapshot accepted")
	}
}

func TestCloseClosesSubscriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latchd.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := s.Subscribe()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Error("subscription open after Close")
	}
}
