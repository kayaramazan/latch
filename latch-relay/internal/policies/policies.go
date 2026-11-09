// Package policies is the relay's policy layer: it persists each token's
// PolicySync, compiles it into a shared policy.Engine, and evaluates requests
// at ingress. Policies are enforced even while the agent is offline.
package policies

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Manager holds compiled engines per token and persists snapshots.
type Manager struct {
	store   *store.Store
	trusted []string
	geo     *policy.GeoDB
	geoPath string
	log     *slog.Logger

	mu      sync.RWMutex
	byToken map[string]*tokenPolicy
}

type tokenPolicy struct {
	version    int
	engine     *policy.Engine
	geoMissing bool
}

// Options configure a Manager.
type Options struct {
	Store          *store.Store
	TrustedProxies []string
	GeoIPPath      string // optional mmdb path
	Logger         *slog.Logger
}

// New builds a Manager and opens the GeoIP database if configured.
func New(opts Options) (*Manager, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	m := &Manager{store: opts.Store, trusted: opts.TrustedProxies, geoPath: opts.GeoIPPath,
		log: opts.Logger, byToken: map[string]*tokenPolicy{}}
	if opts.GeoIPPath != "" {
		geo, err := policy.OpenGeoDB(opts.GeoIPPath)
		if err != nil {
			opts.Logger.Warn("geoip database unavailable; country rules will not match", "err", err)
		} else {
			m.geo = geo
		}
	}
	return m, nil
}

// Load rebuilds engines from persisted snapshots (call at startup).
func (m *Manager) Load(ctx context.Context) error {
	recs, err := m.store.AllPolicies(ctx)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if _, err := m.compile(rec.TokenID, rec.Version, &rec.Snapshot); err != nil {
			m.log.Warn("skipping unparsable persisted policy", "token", rec.TokenID, "err", err)
		}
	}
	return nil
}

// Sync validates, persists and installs a token's policy snapshot. It returns
// the acknowledged version and whether a country rule is present without a
// GeoIP database.
func (m *Manager) Sync(ctx context.Context, tokenID string, ps *tunnel.PolicySync) (version int, geoMissing bool, err error) {
	tp, err := m.compile(tokenID, ps.Version, ps)
	if err != nil {
		return 0, false, err
	}
	if err := m.store.SavePolicy(ctx, tokenID, ps); err != nil {
		return 0, false, err
	}
	return tp.version, tp.geoMissing, nil
}

func (m *Manager) compile(tokenID string, version int, ps *tunnel.PolicySync) (*tokenPolicy, error) {
	bindings := make(map[string]policy.Binding, len(ps.Bindings))
	for _, b := range ps.Bindings {
		bindings[b.RouteID] = policy.Binding{IPPolicyID: b.IPPolicyID, RateLimitIDs: b.RateLimitIDs, Headers: b.Headers}
	}
	eng, err := policy.New(policy.Options{
		TrustedProxies: m.trusted, Where: "relay", GeoIP: m.geo, GeoRequired: true,
	}, ps.IPPolicies, ps.RateLimits, bindings)
	if err != nil {
		return nil, err
	}
	tp := &tokenPolicy{version: version, engine: eng, geoMissing: eng.GeoMissing()}
	m.mu.Lock()
	m.byToken[tokenID] = tp
	m.mu.Unlock()
	return tp, nil
}

// Evaluate runs the token's policy for a request. With no policy it allows.
func (m *Manager) Evaluate(tokenID, routeID string, req policy.Request, now time.Time) policy.Decision {
	m.mu.RLock()
	tp := m.byToken[tokenID]
	m.mu.RUnlock()
	if tp == nil {
		return policy.Decision{Allow: true, Where: "relay", ClientIP: policy.ClientIP(req.RemoteAddr, req.Header, m.trusted)}
	}
	return tp.engine.Evaluate(routeID, req, now)
}

// ApplyRequest applies a route's request-phase header rules.
func (m *Manager) ApplyRequest(tokenID, routeID string, h http.Header) {
	m.mu.RLock()
	tp := m.byToken[tokenID]
	m.mu.RUnlock()
	if tp != nil {
		tp.engine.ApplyRequest(routeID, h)
	}
}

// ApplyResponse applies a route's response-phase header rules.
func (m *Manager) ApplyResponse(tokenID, routeID string, h http.Header) {
	m.mu.RLock()
	tp := m.byToken[tokenID]
	m.mu.RUnlock()
	if tp != nil {
		tp.engine.ApplyResponse(routeID, h)
	}
}

// Version returns the installed policy version for a token (0 if none).
func (m *Manager) Version(tokenID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tp := m.byToken[tokenID]; tp != nil {
		return tp.version
	}
	return 0
}

// Drop removes a token's policy from memory and storage (after revocation).
func (m *Manager) Drop(ctx context.Context, tokenID string) {
	m.mu.Lock()
	delete(m.byToken, tokenID)
	m.mu.Unlock()
	_ = m.store.DeletePolicy(ctx, tokenID)
}

// Close releases the GeoIP database.
func (m *Manager) Close() error {
	if m.geo != nil {
		return m.geo.Close()
	}
	return nil
}
