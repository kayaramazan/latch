// Package tunnel is the latchd side of the Latch tunnel protocol: a client per
// configured relay that announces the local tunnel routes, serves data streams
// by dialing local targets, and reconnects with exponential backoff.
package tunnel

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Deps are the Manager's collaborators.
type Deps struct {
	Store        *store.Store
	AgentID      string
	AgentVersion string
	Logger       *slog.Logger
}

// Manager keeps one Client per configured relay in sync with the store.
type Manager struct {
	deps Deps
	log  *slog.Logger

	events *EventBuffer

	mu      sync.Mutex
	clients map[string]*handle
}

type handle struct {
	client *Client
	relay  model.Relay
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager builds a Manager.
func NewManager(d Deps) *Manager {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Manager{deps: d, log: d.Logger, events: NewEventBuffer(DefaultEventBufferSize), clients: map[string]*handle{}}
}

// Events returns the shared AccessEvent buffer (relay-pushed events).
func (m *Manager) Events() *EventBuffer { return m.events }

// Run reconciles clients with the store and keeps them running until ctx is
// cancelled. It blocks.
func (m *Manager) Run(ctx context.Context) {
	changes, cancelSub := m.deps.Store.Subscribe()
	defer cancelSub()

	snap, version := m.deps.Store.Current()
	m.reconcile(ctx, snap, version)

	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case ch, ok := <-changes:
			if !ok {
				m.stopAll()
				return
			}
			m.reconcile(ctx, ch.Snapshot, ch.Version)
		}
	}
}

// reconcile creates, updates or removes clients to match the snapshot.
func (m *Manager) reconcile(ctx context.Context, snap *model.Snapshot, version int) {
	if snap == nil {
		return
	}
	desired := map[string]model.Relay{}
	for _, r := range snap.Relays {
		desired[r.ID] = r
	}
	routesByRelay := map[string][]model.Route{}
	for _, rt := range snap.Routes {
		if rt.Exposure == model.ExposureTunnel && rt.Enabled && rt.RelayID != "" {
			routesByRelay[rt.RelayID] = append(routesByRelay[rt.RelayID], rt)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove clients whose relay is gone.
	for id, h := range m.clients {
		if _, ok := desired[id]; !ok {
			m.log.Info("relay removed, stopping client", "relay", h.relay.Name, "relayId", id)
			h.cancel()
			delete(m.clients, id)
		}
	}

	for id, relay := range desired {
		specs, targets := buildRoutes(routesByRelay[id])
		policy := buildPolicy(snap, id, version)
		h, ok := m.clients[id]
		if ok && relayEqual(h.relay, relay) {
			h.client.SetRoutes(specs, targets)
			h.client.SetPolicy(policy)
			continue
		}
		if ok {
			// Connection parameters changed; restart the client.
			m.log.Info("relay settings changed, restarting client", "relay", relay.Name, "relayId", id)
			h.cancel()
			delete(m.clients, id)
		}
		cl := NewClient(relay, m.deps.AgentID, m.deps.AgentVersion, m.events, m.log)
		cl.SetRoutes(specs, targets)
		cl.SetPolicy(policy)
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		nh := &handle{client: cl, relay: relay, cancel: cancel, done: done}
		m.clients[id] = nh
		go func() {
			defer close(done)
			cl.Run(cctx)
		}()
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	handles := make([]*handle, 0, len(m.clients))
	for _, h := range m.clients {
		h.cancel()
		handles = append(handles, h)
	}
	m.clients = map[string]*handle{}
	m.mu.Unlock()
	for _, h := range handles {
		<-h.done
	}
}

// Status returns one ClientStatus per configured relay, sorted by relay name.
func (m *Manager) Status() []ClientStatus {
	m.mu.Lock()
	out := make([]ClientStatus, 0, len(m.clients))
	for _, h := range m.clients {
		out = append(out, h.client.Status())
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].RelayName == out[j].RelayName {
			return out[i].RelayID < out[j].RelayID
		}
		return out[i].RelayName < out[j].RelayName
	})
	return out
}

// buildRoutes turns tunnel routes into announce specs and a routeID→target map.
func buildRoutes(routes []model.Route) ([]tunnel.RouteSpec, map[string]string) {
	specs := make([]tunnel.RouteSpec, 0, len(routes))
	targets := map[string]string{}
	for _, rt := range routes {
		spec := tunnel.RouteSpec{RouteID: rt.ID, Kind: rt.Kind, PathPrefix: rt.PathPrefix}
		if rt.Kind == model.KindTCP {
			spec.TCPPort = rt.TCPPort
		} else {
			spec.Hostnames = rt.Hostnames
		}
		specs = append(specs, spec)
		targets[rt.ID] = rt.Target
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].RouteID < specs[j].RouteID })
	return specs, targets
}

// buildPolicy assembles the PolicySync for one relay: the IP policies and rate
// limits referenced by its routes (plus all global ones), and a binding per
// route. Version is the config version so the relay can detect staleness.
func buildPolicy(snap *model.Snapshot, relayID string, version int) tunnel.PolicySync {
	ipByID := map[string]model.IPPolicy{}
	for _, p := range snap.IPPolicies {
		ipByID[p.ID] = p
	}
	rlByID := map[string]model.RateLimitRule{}
	for _, r := range snap.RateLimits {
		rlByID[r.ID] = r
	}
	neededIP := map[string]bool{}
	neededRL := map[string]bool{}
	var bindings []tunnel.PolicyBinding
	for _, rt := range snap.Routes {
		if rt.Exposure != model.ExposureTunnel || rt.RelayID != relayID || !rt.Enabled {
			continue
		}
		b := tunnel.PolicyBinding{RouteID: rt.ID, IPPolicyID: rt.IPPolicyID, RateLimitIDs: rt.RateLimitIDs, Headers: rt.Headers}
		bindings = append(bindings, b)
		if rt.IPPolicyID != "" {
			neededIP[rt.IPPolicyID] = true
		}
		for _, id := range rt.RateLimitIDs {
			neededRL[id] = true
		}
	}
	var ips []model.IPPolicy
	for _, p := range snap.IPPolicies {
		if p.Global || neededIP[p.ID] {
			ips = append(ips, p)
		}
	}
	var rls []model.RateLimitRule
	for _, r := range snap.RateLimits {
		if r.Global || neededRL[r.ID] {
			rls = append(rls, r)
		}
	}
	return tunnel.PolicySync{Version: version, IPPolicies: ips, RateLimits: rls, Bindings: bindings}
}

// relayEqual reports whether two relays have the same connection parameters.
func relayEqual(a, b model.Relay) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Host == b.Host && a.Port == b.Port &&
		a.Token == b.Token && a.CAPEM == b.CAPEM && a.BaseDomain == b.BaseDomain
}
