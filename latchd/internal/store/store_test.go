package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

func open(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "latchd.db")
	s, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func addRelay(t *testing.T, s *Store) model.Relay {
	t.Helper()
	relay := model.NewRelay("vps", "relay.example.com", "tok")
	_, err := s.Update(context.Background(), "test", "add relay", func(snap *model.Snapshot) error {
		snap.Relays = append(snap.Relays, relay)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return relay
}

func addRoute(t *testing.T, s *Store, name, host string) model.Route {
	t.Helper()
	r := model.NewRoute(name, "127.0.0.1:8080", model.ExposureLocal)
	r.Hostnames = []string{host}
	_, err := s.Update(context.Background(), "test", "", func(snap *model.Snapshot) error {
		snap.Routes = append(snap.Routes, r)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenCreatesInitialVersionWithPermissions(t *testing.T) {
	s, path := open(t, Options{})
	snap, v := s.Current()
	if v != 1 || len(snap.Routes) != 0 || snap.Settings.Listen != "127.0.0.1:4040" {
		t.Fatalf("initial state: v=%d snap=%+v", v, snap)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("db perm = %o, want 600", info.Mode().Perm())
		}
	}
	sv, err := s.SchemaVersion(context.Background())
	if err != nil || sv != len(migrations) {
		t.Errorf("schema version = %d, %v", sv, err)
	}
}

func TestUpdateCreatesVersionsAndStampsRoutes(t *testing.T) {
	s, _ := open(t, Options{})
	ctx := context.Background()
	r := addRoute(t, s, "site", "site.example.com")
	snap, v := s.Current()
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	got := snap.Route(r.ID)
	if got == nil || got.Version != 2 || got.CreatedAt.IsZero() {
		t.Fatalf("route not stamped: %+v", got)
	}
	created := got.CreatedAt

	res, err := s.Update(ctx, "test", "", func(snap *model.Snapshot) error {
		snap.Route(r.ID).Target = "127.0.0.1:9090"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 3 || res.Diff.Summary() != "1 changed" || len(res.Diff.AffectedRoutes) != 1 {
		t.Errorf("result = %+v", res)
	}
	got = res.Snapshot.Route(r.ID)
	if got.Version != 3 || !got.CreatedAt.Equal(created) {
		t.Errorf("stamp after change: %+v", got)
	}

	// Unrelated change: route keeps its version.
	res, err = s.Update(ctx, "test", "", func(snap *model.Snapshot) error {
		snap.Settings.Log.Level = "debug"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Snapshot.Route(r.ID).Version != 3 {
		t.Error("untouched route was re-stamped")
	}

	vs, err := s.Versions(ctx, 10, 0)
	if err != nil || len(vs) != 4 || vs[0].Version != 4 || vs[3].Summary != "initial configuration" {
		t.Errorf("versions = %+v, %v", vs, err)
	}
	if vs[2].Summary != "1 added" {
		t.Errorf("auto summary = %q", vs[2].Summary)
	}
}

func TestUpdateRejectsInvalidAndKeepsCurrent(t *testing.T) {
	s, _ := open(t, Options{})
	_, v := s.Current()
	_, err := s.Update(context.Background(), "test", "", func(snap *model.Snapshot) error {
		r := model.NewRoute("bad", "nope", model.ExposureTunnel)
		snap.Routes = append(snap.Routes, r)
		return nil
	})
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	paths := map[string]bool{}
	for _, f := range ve.Fields {
		paths[f.Path] = true
	}
	if !paths["routes[0].target"] || !paths["routes[0].relayId"] || !paths["routes[0].hostnames"] {
		t.Errorf("unexpected fields: %v", ve.Fields)
	}
	snap, v2 := s.Current()
	if v2 != v || len(snap.Routes) != 0 {
		t.Error("invalid update changed state")
	}

	// fn error aborts.
	boom := errors.New("boom")
	if _, err := s.Update(context.Background(), "t", "", func(*model.Snapshot) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("fn error not propagated: %v", err)
	}
	// No-op does not create a version.
	if _, err := s.Update(context.Background(), "t", "", func(*model.Snapshot) error { return nil }); !errors.Is(err, ErrNoChanges) {
		t.Errorf("no-op: %v", err)
	}
	if _, v3 := s.Current(); v3 != v {
		t.Error("no-op created a version")
	}
}

func TestCurrentReturnsCopy(t *testing.T) {
	s, _ := open(t, Options{})
	addRoute(t, s, "a", "a.example.com")
	snap, _ := s.Current()
	snap.Routes[0].Name = "mutated"
	again, _ := s.Current()
	if again.Routes[0].Name != "a" {
		t.Error("Current exposed internal state")
	}
}

func TestRollbackAndDiff(t *testing.T) {
	s, _ := open(t, Options{})
	ctx := context.Background()
	a := addRoute(t, s, "a", "a.example.com") // v2
	addRoute(t, s, "b", "b.example.com")      // v3

	d, err := s.Diff(ctx, 2, 3)
	if err != nil || len(d.Added) != 1 || d.Added[0].Name != "b" {
		t.Fatalf("diff 2→3 = %+v, %v", d, err)
	}

	res, err := s.Rollback(ctx, 2, "test")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != 4 || len(res.Snapshot.Routes) != 1 || res.Snapshot.Routes[0].ID != a.ID {
		t.Errorf("rollback result = %+v", res)
	}
	if len(res.Diff.Removed) != 1 {
		t.Errorf("rollback diff = %+v", res.Diff)
	}
	_, info, err := s.Version(ctx, 4)
	if err != nil || info.Summary != "rollback to version 2" {
		t.Errorf("version 4 info = %+v, %v", info, err)
	}
	if _, _, err := s.Version(ctx, 99); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("unknown version: %v", err)
	}
	// Rolling back to the identical state is a no-op.
	if _, err := s.Rollback(ctx, 2, "test"); !errors.Is(err, ErrNoChanges) {
		t.Errorf("identical rollback: %v", err)
	}
}

func TestExportImport(t *testing.T) {
	src, _ := open(t, Options{})
	ctx := context.Background()
	relay := addRelay(t, src)
	a := addRoute(t, src, "a", "a.example.com")
	exported, _ := src.Export()
	if exported.Relays[0].Token != "tok" {
		t.Fatal("export must include secrets")
	}

	dst, _ := open(t, Options{})
	res, err := dst.Import(ctx, exported, ImportReplace, "test")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := dst.Export()
	if len(got.Routes) != 1 || got.Routes[0].ID != a.ID || got.Relays[0].ID != relay.ID || res.Version != 2 {
		t.Errorf("replace import: %+v", got)
	}

	// Merge: existing object updated by id, new one added, others kept.
	addRoute(t, dst, "local-only", "local.example.com")
	exported.Routes[0].Name = "renamed"
	if _, err := dst.Import(ctx, exported, ImportMerge, "test"); err != nil {
		t.Fatal(err)
	}
	got, _ = dst.Export()
	if len(got.Routes) != 2 || got.Route(a.ID).Name != "renamed" {
		t.Errorf("merge import: %+v", got.Routes)
	}

	// Invalid import is rejected and state unchanged.
	bad := *exported
	bad.Settings.Listen = "garbage"
	_, before := dst.Current()
	if _, err := dst.Import(ctx, &bad, ImportReplace, "test"); err == nil {
		t.Error("invalid import accepted")
	}
	if _, after := dst.Current(); after != before {
		t.Error("invalid import changed version")
	}
	if _, err := dst.Import(ctx, exported, ImportMode("weird"), "t"); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latchd.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := addRoute(t, s, "a", "a.example.com")
	_ = s.Close()

	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	snap, v := s2.Current()
	if v != 2 || snap.Route(r.ID) == nil {
		t.Errorf("after reopen: v=%d routes=%d", v, len(snap.Routes))
	}
	vs, _ := s2.Versions(context.Background(), 10, 0)
	if len(vs) != 2 {
		t.Errorf("versions after reopen = %d", len(vs))
	}
}

func TestPruneKeepsMaxVersions(t *testing.T) {
	s, _ := open(t, Options{MaxVersions: 3})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := s.Update(ctx, "t", "", func(snap *model.Snapshot) error {
			snap.Settings.Events.RetentionDays = 10 + i
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	vs, _ := s.Versions(ctx, 100, 0)
	if len(vs) != 3 || vs[0].Version != 6 || vs[2].Version != 4 {
		t.Errorf("kept versions = %+v", vs)
	}
	if _, _, err := s.Version(ctx, 1); !errors.Is(err, ErrVersionNotFound) {
		t.Error("pruned version still readable")
	}
	page, _ := s.Versions(ctx, 1, 6)
	if len(page) != 1 || page[0].Version != 5 {
		t.Errorf("pagination: %+v", page)
	}
}

func TestSubscribe(t *testing.T) {
	s, _ := open(t, Options{})
	ch, cancel := s.Subscribe()
	defer cancel()
	r := addRoute(t, s, "a", "a.example.com")
	select {
	case c := <-ch:
		if c.Version != 2 || len(c.Diff.AffectedRoutes) != 1 || c.Snapshot.Route(r.ID) == nil {
			t.Errorf("change = %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("no change delivered")
	}
	cancel()
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after cancel")
	}
	cancel() // idempotent

	// Slow subscriber is dropped, writers never block.
	slow, slowCancel := s.Subscribe()
	defer slowCancel()
	for i := 0; i < 70; i++ {
		_, _ = s.Update(context.Background(), "t", "", func(snap *model.Snapshot) error {
			snap.Settings.Events.RetentionDays = 20 + i
			return nil
		})
	}
	n := 0
	for range slow {
		n++
	}
	if n != 64 {
		t.Errorf("slow subscriber got %d changes before being dropped, want 64", n)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s, _ := open(t, Options{})
	ctx := context.Background()
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			r := model.NewRoute("r", "127.0.0.1:1", model.ExposureLocal)
			r.Name = "r" + string(rune('a'+i))
			r.Hostnames = []string{r.Name + ".example.com"}
			_, err := s.Update(ctx, "t", "", func(snap *model.Snapshot) error {
				snap.Routes = append(snap.Routes, r)
				return nil
			})
			done <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	snap, v := s.Current()
	if v != 21 || len(snap.Routes) != 20 {
		t.Errorf("v=%d routes=%d", v, len(snap.Routes))
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latchd.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (999, 'x')`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path, Options{}); err == nil {
		t.Fatal("newer schema should be refused")
	}
}
