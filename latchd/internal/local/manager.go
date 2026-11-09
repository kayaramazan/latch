package local

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/caddyserver/caddy/v2"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/model"
)

// Deps are the Manager's collaborators.
type Deps struct {
	Store     *store.Store
	Emit      func(model.AccessEvent) // sink for local AccessEvents (agent ring)
	Options   Options                 // ports, issuer, storage dir, ACME
	Trusted   []string
	GeoIPPath string
	Logger    *slog.Logger
}

// Manager keeps Caddy's config in sync with the store's local routes.
type Manager struct {
	deps Deps
	log  *slog.Logger
	geo  *policy.GeoDB

	mu      sync.Mutex
	started bool
	lastErr string
	health  *healthChecker
}

// NewManager builds a Manager.
func NewManager(d Deps) *Manager {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	m := &Manager{deps: d, log: d.Logger, health: newHealthChecker(d.Logger)}
	if d.GeoIPPath != "" {
		if geo, err := policy.OpenGeoDB(d.GeoIPPath); err != nil {
			d.Logger.Warn("local mode: geoip unavailable", "err", err)
		} else {
			m.geo = geo
		}
	}
	return m
}

// Run applies the current config and reapplies on every store change until ctx
// is cancelled, then stops Caddy.
func (m *Manager) Run(ctx context.Context) {
	changes, cancel := m.deps.Store.Subscribe()
	defer cancel()

	go m.health.Run(ctx)

	snap, version := m.deps.Store.Current()
	if err := m.apply(snap, version); err != nil {
		m.log.Error("local mode: initial config failed", "err", err)
	}
	m.health.Update(snap)

	for {
		select {
		case <-ctx.Done():
			m.stop()
			return
		case ch, ok := <-changes:
			if !ok {
				m.stop()
				return
			}
			if err := m.apply(ch.Snapshot, ch.Version); err != nil {
				m.log.Error("local mode: config apply failed, keeping previous", "err", err)
			}
			m.health.Update(ch.Snapshot)
		}
	}
}

// apply builds the policy engine and Caddy config for the snapshot's local
// routes and loads it. On error the previous Caddy config keeps running.
func (m *Manager) apply(snap *model.Snapshot, version int) error {
	engine, err := m.buildEngine(snap)
	if err != nil {
		return err
	}
	SetRuntime(&Runtime{Engine: engine, Emit: m.deps.Emit, Trusted: m.deps.Trusted})

	if err := m.checkPorts(snap); err != nil {
		m.setErr(err.Error())
		return err
	}

	cfgJSON, err := Generate(snap, m.deps.Options)
	if err != nil {
		return err
	}

	hasLocal := len(localRoutes(snap)) > 0
	m.mu.Lock()
	defer m.mu.Unlock()
	if !hasLocal && !m.started {
		// Local mode is unused; do not start Caddy or bind any ports.
		return nil
	}
	if err := caddy.Load(cfgJSON, false); err != nil {
		m.lastErr = caddyError(err, snap)
		return fmt.Errorf("local: load config: %w", err)
	}
	if !m.started {
		m.started = true
		m.log.Info("local mode: Caddy started", "httpsPort", m.deps.Options.HTTPSPort)
	}
	m.lastErr = ""
	n := len(localRoutes(snap))
	m.log.Info("local mode config applied", "localRoutes", n, "configVersion", version)
	return nil
}

func (m *Manager) stop() {
	m.mu.Lock()
	started := m.started
	m.started = false
	m.mu.Unlock()
	if started {
		_ = caddy.Stop()
	}
	if m.geo != nil {
		_ = m.geo.Close()
	}
}

// buildEngine compiles the policy engine for local routes.
func (m *Manager) buildEngine(snap *model.Snapshot) (*policy.Engine, error) {
	ipPolicies, rateRules, bindings := localPolicyInputs(snap)
	if len(bindings) == 0 && len(ipPolicies) == 0 && len(rateRules) == 0 {
		return nil, nil
	}
	return policy.New(policy.Options{
		TrustedProxies: m.deps.Trusted, Where: "local", GeoIP: m.geo, GeoRequired: true,
	}, ipPolicies, rateRules, bindings)
}

// checkPorts gives a clear error on an obvious listen conflict.
func (m *Manager) checkPorts(snap *model.Snapshot) error {
	if len(localRoutes(snap)) == 0 {
		return nil
	}
	mgmtHost, mgmtPort, _ := net.SplitHostPort(snap.Settings.Listen)
	_ = mgmtHost
	for _, p := range []int{m.deps.Options.HTTPPort, m.deps.Options.HTTPSPort} {
		if p == 0 {
			continue
		}
		if mgmtPort == fmt.Sprint(p) {
			return fmt.Errorf("local mode port %d conflicts with the management API (settings.listen=%s); change one", p, snap.Settings.Listen)
		}
	}
	return nil
}

func (m *Manager) setErr(s string) {
	m.mu.Lock()
	m.lastErr = s
	m.mu.Unlock()
}

// LastError returns the most recent apply error ("" when healthy).
func (m *Manager) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Health returns per-route upstream health.
func (m *Manager) Health() map[string]string { return m.health.Snapshot() }

// caddyError wraps a Caddy load error with the local hostnames for context.
func caddyError(err error, snap *model.Snapshot) string {
	var hosts []string
	for _, rt := range localRoutes(snap) {
		hosts = append(hosts, rt.Hostnames...)
	}
	return fmt.Sprintf("caddy rejected the config for %v: %v", hosts, err)
}
