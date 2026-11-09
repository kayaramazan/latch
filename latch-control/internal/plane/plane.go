// Package plane is the S1 skeleton of latch-control: it receives relay
// heartbeats (fleet inventory + usage) and drives a relay's control API over
// mTLS to mint tokens. Single-tenant, in-memory; accounts/billing are S2/S3.
package plane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kayaramazan/latch/proto/control"
)

// RelayRecord is one relay in the fleet inventory.
type RelayRecord struct {
	RelayID    string         `json:"relayId"`
	Region     string         `json:"region"`
	Version    string         `json:"version"`
	ControlURL string         `json:"controlUrl"`
	Capacity   int            `json:"capacity"`
	Health     control.Health `json:"health"`
	LastSeen   time.Time      `json:"lastSeen"`
}

// Plane is the control-plane skeleton.
type Plane struct {
	regToken   string
	clientCert tls.Certificate
	relayCAs   *x509.CertPool
	log        *slog.Logger

	mu    sync.Mutex
	fleet map[string]*RelayRecord
}

// Options configure a Plane.
type Options struct {
	RegToken   string          // bearer token relays present on heartbeat
	ClientCert tls.Certificate // the control plane's client cert for relay mTLS
	RelayCAs   *x509.CertPool  // trusts relay control servers
	Logger     *slog.Logger
}

// New builds a Plane.
func New(o Options) *Plane {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Plane{regToken: o.RegToken, clientCert: o.ClientCert, relayCAs: o.RelayCAs,
		log: o.Logger, fleet: map[string]*RelayRecord{}}
}

// Handler is the control plane's private HTTP API.
func (p *Plane) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /relays/heartbeat", p.heartbeat)
	mux.HandleFunc("GET /relays", p.listRelays)
	mux.HandleFunc("POST /relays/{id}/tokens", p.createToken)
	return mux
}

func (p *Plane) heartbeat(w http.ResponseWriter, r *http.Request) {
	if p.regToken != "" && r.Header.Get("Authorization") != "Bearer "+p.regToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var hb control.Heartbeat
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if hb.RelayID == "" {
		http.Error(w, "relayId required", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.fleet[hb.RelayID] = &RelayRecord{
		RelayID: hb.RelayID, Region: hb.Region, Version: hb.Version,
		ControlURL: normalizeURL(hb.ControlURL), Capacity: hb.Capacity,
		Health: hb.Health, LastSeen: time.Now().UTC(),
	}
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plane) listRelays(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	out := make([]*RelayRecord, 0, len(p.fleet))
	for _, r := range p.fleet {
		out = append(out, r)
	}
	p.mu.Unlock()
	writeJSON(w, out)
}

func (p *Plane) createToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p.mu.Lock()
	rec := p.fleet[id]
	p.mu.Unlock()
	if rec == nil {
		http.Error(w, "unknown relay", http.StatusNotFound)
		return
	}
	var req control.CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	res, err := p.client(rec.ControlURL).CreateToken(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, res)
}

// client builds a control client for a relay's control URL.
func (p *Plane) client(controlURL string) *control.Client {
	return control.NewClient(controlURL, p.clientCert, p.relayCAs)
}

// Relays returns the current inventory (for the CLI/tests).
func (p *Plane) Relays() []*RelayRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*RelayRecord, 0, len(p.fleet))
	for _, r := range p.fleet {
		out = append(out, r)
	}
	return out
}

// CreateTokenOn mints a token on a relay by id (for the CLI/tests).
func (p *Plane) CreateTokenOn(ctx context.Context, relayID string, req control.CreateTokenRequest) (*control.CreateTokenResponse, error) {
	p.mu.Lock()
	rec := p.fleet[relayID]
	p.mu.Unlock()
	if rec == nil {
		return nil, context.Canceled
	}
	return p.client(rec.ControlURL).CreateToken(ctx, req)
}

func normalizeURL(s string) string {
	if s == "" {
		return s
	}
	if !strings.Contains(s, "://") {
		return "https://" + s
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// PickRelay chooses a relay in region with the fewest tokens (simple placement).
// region "" matches any. It returns the relay id and whether one was found.
func (p *Plane) PickRelay(region string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	best := ""
	bestLoad := int(^uint(0) >> 1)
	for id, r := range p.fleet {
		if region != "" && r.Region != region {
			continue
		}
		if r.Health.Tokens < bestLoad {
			bestLoad = r.Health.Tokens
			best = id
		}
	}
	return best, best != ""
}

// RelayFor returns a control client + the relay's base domain for relayID.
func (p *Plane) RelayFor(relayID string) (*control.Client, string, bool) {
	p.mu.Lock()
	rec := p.fleet[relayID]
	p.mu.Unlock()
	if rec == nil {
		return nil, "", false
	}
	return p.client(rec.ControlURL), rec.Health.BaseDomain, true
}
