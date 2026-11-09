package local

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

// Upstream health states.
const (
	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// healthChecker probes the upstream targets of routes with health checks
// enabled (both local and tunnel routes) and tracks their status.
type healthChecker struct {
	log *slog.Logger
	mu  sync.Mutex
	// routeID -> probe spec
	targets map[string]probe
	status  map[string]string
}

type probe struct {
	target   string
	path     string
	interval time.Duration
	timeout  time.Duration
}

func newHealthChecker(log *slog.Logger) *healthChecker {
	return &healthChecker{log: log, targets: map[string]probe{}, status: map[string]string{}}
}

// Update replaces the set of probed routes from a snapshot.
func (h *healthChecker) Update(snap *model.Snapshot) {
	next := map[string]probe{}
	for _, rt := range snap.Routes {
		if !rt.Enabled || !rt.HealthCheck.Enabled || rt.Target == "" {
			continue
		}
		iv := time.Duration(rt.HealthCheck.IntervalSec) * time.Second
		if iv <= 0 {
			iv = 15 * time.Second
		}
		to := time.Duration(rt.HealthCheck.TimeoutSec) * time.Second
		if to <= 0 {
			to = 5 * time.Second
		}
		next[rt.ID] = probe{target: rt.Target, path: rt.HealthCheck.Path, interval: iv, timeout: to}
	}
	h.mu.Lock()
	h.targets = next
	for id := range h.status {
		if _, ok := next[id]; !ok {
			delete(h.status, id)
		}
	}
	for id := range next {
		if _, ok := h.status[id]; !ok {
			h.status[id] = HealthUnknown
		}
	}
	h.mu.Unlock()
}

// Run probes all targets on a shared ticker until ctx is cancelled.
func (h *healthChecker) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.probeAll()
		}
	}
}

func (h *healthChecker) probeAll() {
	h.mu.Lock()
	probes := make(map[string]probe, len(h.targets))
	for id, p := range h.targets {
		probes[id] = p
	}
	h.mu.Unlock()
	for id, p := range probes {
		status := probeOnce(p)
		h.mu.Lock()
		h.status[id] = status
		h.mu.Unlock()
	}
}

func probeOnce(p probe) string {
	if p.path != "" {
		client := &http.Client{Timeout: p.timeout}
		url := "http://" + p.target + p.path
		resp, err := client.Get(url)
		if err != nil {
			return HealthUnhealthy
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return HealthHealthy
		}
		return HealthUnhealthy
	}
	conn, err := net.DialTimeout("tcp", p.target, p.timeout)
	if err != nil {
		return HealthUnhealthy
	}
	_ = conn.Close()
	return HealthHealthy
}

// Snapshot returns the current status by route ID.
func (h *healthChecker) Snapshot() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.status))
	for k, v := range h.status {
		out[k] = v
	}
	return out
}
