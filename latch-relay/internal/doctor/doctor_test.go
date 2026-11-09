package doctor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeResolver struct {
	hosts map[string][]string
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f.hosts[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func TestDNSCheck(t *testing.T) {
	r := fakeResolver{hosts: map[string][]string{"x.relay.example.com": {"203.0.113.10"}}}
	if c := DNSCheck(context.Background(), r, "x.relay.example.com", "203.0.113.10"); !c.OK {
		t.Errorf("expected OK, got %+v", c)
	}
	if c := DNSCheck(context.Background(), r, "x.relay.example.com", "198.51.100.1"); c.OK {
		t.Errorf("expected failure for wrong IP, got %+v", c)
	}
	if c := DNSCheck(context.Background(), r, "missing.example.com", ""); c.OK {
		t.Errorf("expected failure for missing name, got %+v", c)
	}
	if c := DNSCheck(context.Background(), r, "x.relay.example.com", ""); !c.OK {
		t.Errorf("expected OK for resolvable name without wantIP, got %+v", c)
	}
}

func TestClockCheck(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if c := ClockCheck(now, now.Add(2*time.Second), 30*time.Second); !c.OK {
		t.Errorf("small skew should pass, got %+v", c)
	}
	if c := ClockCheck(now, now.Add(10*time.Minute), 30*time.Second); c.OK {
		t.Errorf("large skew should fail, got %+v", c)
	}
}

func TestSummarize(t *testing.T) {
	if !Summarize([]Check{{OK: true, Fatal: true}, {OK: false, Fatal: false}}) {
		t.Error("non-fatal failure should not fail the summary")
	}
	if Summarize([]Check{{OK: false, Fatal: true}}) {
		t.Error("fatal failure should fail the summary")
	}
}

func TestPortFree(t *testing.T) {
	// A high ephemeral port should be bindable.
	if c := PortFree("127.0.0.1:0"); !c.OK {
		t.Errorf("ephemeral port should be free, got %+v", c)
	}
}
