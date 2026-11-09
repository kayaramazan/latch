package events

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

func openTest(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ev(routeID string, status int, deny string) model.AccessEvent {
	e := model.AccessEvent{TS: time.Now().UTC(), Source: model.SourceRelay, RouteID: routeID,
		Hostname: "app.test", ClientIP: "203.0.113.5", Method: "GET", Path: "/x", Status: status,
		DurationMs: 10, Decision: model.DecisionAllow, Where: "relay"}
	if deny != "" {
		e.Decision, e.DenyReason, e.Status = model.DecisionDeny, deny, 403
	}
	return e
}

// flush waits for the async writer to persist queued events.
func flush(t *testing.T, s *Store, wantAtLeast int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err == nil && n >= wantAtLeast {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("events not persisted within timeout (wanted >= %d)", wantAtLeast)
}

func TestAppendRingAndRecent(t *testing.T) {
	s := openTest(t, Options{RingSize: 4})
	for i := 0; i < 6; i++ {
		s.Append(ev("rt_a", 200, ""))
	}
	if got := s.Recent(10); len(got) != 4 {
		t.Errorf("ring should cap at 4, got %d", len(got))
	}
}

func TestQueryFilters(t *testing.T) {
	s := openTest(t, Options{})
	s.Append(ev("rt_a", 200, ""))
	s.Append(ev("rt_b", 200, ""))
	s.Append(ev("rt_a", 403, "ip_policy"))
	flush(t, s, 3)

	all, _ := s.Query(Filter{})
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}
	byRoute, _ := s.Query(Filter{RouteID: "rt_a"})
	if len(byRoute) != 2 {
		t.Errorf("route filter: got %d", len(byRoute))
	}
	denied, _ := s.Query(Filter{Decision: "deny"})
	if len(denied) != 1 || denied[0].DenyReason != "ip_policy" {
		t.Errorf("deny filter: %+v", denied)
	}
}

func TestSubscribeRouteFilter(t *testing.T) {
	s := openTest(t, Options{})
	ch, cancel := s.Subscribe("rt_a")
	defer cancel()
	s.Append(ev("rt_b", 200, "")) // filtered out
	s.Append(ev("rt_a", 200, "")) // delivered
	select {
	case e := <-ch:
		if e.RouteID != "rt_a" {
			t.Errorf("got %s", e.RouteID)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestAggregate(t *testing.T) {
	s := openTest(t, Options{})
	for i := 0; i < 10; i++ {
		s.Append(ev("rt_a", 200, ""))
	}
	for i := 0; i < 3; i++ {
		s.Append(ev("rt_a", 403, "rate_limit"))
	}
	flush(t, s, 13)
	d, err := s.Aggregate(3600)
	if err != nil {
		t.Fatal(err)
	}
	if d.Requests != 13 || d.Denied != 3 {
		t.Errorf("requests=%d denied=%d", d.Requests, d.Denied)
	}
	if d.StatusClasses["2xx"] != 10 || d.StatusClasses["4xx"] != 3 {
		t.Errorf("status classes = %v", d.StatusClasses)
	}
	if len(d.TopRoutes) != 1 || d.TopRoutes[0].Count != 13 {
		t.Errorf("top routes = %+v", d.TopRoutes)
	}
	if len(d.TopBlockedIPs) != 1 || d.TopBlockedIPs[0].Count != 3 {
		t.Errorf("top blocked ips = %+v", d.TopBlockedIPs)
	}
}

func TestSizeRetention(t *testing.T) {
	s := openTest(t, Options{MaxSizeMB: 1, RingSize: 16})
	// Write enough events to exceed ~1 MB, then force enforcement.
	for i := 0; i < 40000; i++ {
		s.Append(ev(fmt.Sprintf("rt_%d", i%8), 200, ""))
	}
	flush(t, s, 1)
	s.enforce()
	size, err := s.dbSizeBytes()
	if err != nil {
		t.Fatal(err)
	}
	if size > 2*1024*1024 {
		t.Errorf("size retention did not bound the db: %d bytes", size)
	}
}

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := percentile(data, 50); p < 5 || p > 6 {
		t.Errorf("p50 = %v", p)
	}
	if p := percentile(data, 99); p < 9 {
		t.Errorf("p99 = %v", p)
	}
	if percentile(nil, 50) != 0 {
		t.Error("empty should be 0")
	}
}
