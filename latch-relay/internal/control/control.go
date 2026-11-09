// Package control serves the relay's private control API: the mTLS, flag-gated
// interface the SaaS control plane uses to drive this relay. It is off unless
// explicitly configured. Self-hosted relays never enable it.
package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/acme"
	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	"github.com/kayaramazan/latch/latch-relay/internal/registry"
	"github.com/kayaramazan/latch/latch-relay/internal/session"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/latch-relay/internal/usage"
	"github.com/kayaramazan/latch/proto/control"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Deps are the relay components the control API exposes.
type Deps struct {
	Auth     *auth.Manager
	Store    *store.Store
	Sessions *session.Manager
	Registry *registry.Registry
	Usage    *usage.Tracker

	RelayID    string
	Region     string
	Version    string
	PublicHost string
	BaseDomain string
	AgentPort  int
	PublicIP   string

	Resolver acme.Resolver // defaults to the system resolver
	Logger   *slog.Logger
}

// Server serves the control API.
type Server struct {
	deps Deps
	mux  *http.ServeMux
	log  *slog.Logger
}

// New builds the control API server.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Resolver == nil {
		d.Resolver = acme.DefaultResolver()
	}
	s := &Server{deps: d, mux: http.NewServeMux(), log: d.Logger}
	p := control.APIVersion
	s.mux.HandleFunc("POST "+p+"/tokens", s.createToken)
	s.mux.HandleFunc("GET "+p+"/tokens", s.listTokens)
	s.mux.HandleFunc("DELETE "+p+"/tokens/{id}", s.revokeToken)
	s.mux.HandleFunc("POST "+p+"/domains", s.createDomain)
	s.mux.HandleFunc("GET "+p+"/domains", s.listDomains)
	s.mux.HandleFunc("DELETE "+p+"/domains/{domain}", s.deleteDomain)
	s.mux.HandleFunc("POST "+p+"/domains/{domain}/verify", s.verifyDomain)
	s.mux.HandleFunc("GET "+p+"/sessions", s.listSessions)
	s.mux.HandleFunc("GET "+p+"/health", s.health)
	return s
}

// Handler exposes the mux (for tests).
func (s *Server) Handler() http.Handler { return s.mux }

// MTLSConfig builds a server tls.Config that requires and verifies a client
// certificate against clientCA (the control plane's CA).
func MTLSConfig(serverCert tls.Certificate, clientCA *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    clientCA,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}

// Serve runs the control API on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	s.log.Info("relay control API listening", "addr", ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// --- handlers ---

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req control.CreateTokenRequest
	if !decode(w, r, &req) {
		return
	}
	scope := tunnel.Scope{Hostnames: req.Hostnames, TCP: req.TCP}
	var exp *time.Time
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeErr(w, 400, "invalid expiresIn: "+err.Error())
			return
		}
		t := time.Now().Add(d)
		exp = &t
	}
	plain, tok, err := s.deps.Auth.Create(r.Context(), req.Name, scope, exp)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ci := tunnel.ConnInfo{Host: s.deps.PublicHost, Port: s.deps.AgentPort, Token: plain, Name: req.Name}
	writeJSON(w, 201, control.CreateTokenResponse{Token: toTokenInfo(tok), ConnectionString: ci.String()})
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.deps.Store.Tokens(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]control.TokenInfo, 0, len(toks))
	for _, t := range toks {
		out = append(out, toTokenInfo(t))
	}
	writeJSON(w, 200, out)
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Auth.Revoke(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var req control.CreateDomainRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Domain == "" {
		writeErr(w, 400, "domain is required")
		return
	}
	if err := s.deps.Store.AddCustomDomain(r.Context(), req.Domain, s.deps.PublicHost, req.Note); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, control.DomainInfo{Domain: req.Domain, Target: s.deps.PublicHost, Note: req.Note})
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	ds, err := s.deps.Store.ListCustomDomains(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]control.DomainInfo, 0, len(ds))
	for _, d := range ds {
		out = append(out, control.DomainInfo{Domain: d.Domain, Target: d.Target, VerifiedAt: d.VerifiedAt, Note: d.Note})
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.RemoveCustomDomain(r.Context(), r.PathValue("domain")); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp := control.VerifyDomainResponse{Domain: domain, Verified: true}
	if err := acme.CheckPointsHere(ctx, s.deps.Resolver, domain, s.deps.PublicHost, s.deps.PublicIP); err != nil {
		resp.Verified = false
		resp.Detail = err.Error()
	} else {
		_ = s.deps.Store.SetCustomDomainVerified(r.Context(), domain, time.Now())
	}
	writeJSON(w, 200, resp)
}

func (s *Server) listSessions(w http.ResponseWriter, _ *http.Request) {
	out := make([]control.SessionInfo, 0)
	for _, si := range s.deps.Sessions.Sessions() {
		out = append(out, control.SessionInfo{
			ID: si.ID, TokenID: si.TokenID, TokenName: si.TokenName, AgentID: si.AgentID,
			RemoteAddr: si.RemoteAddr, ConnectedAt: si.ConnectedAt, RTTMs: si.RTTMs,
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Health(r.Context()))
}

// Health builds the relay's self-report (also used for heartbeats).
func (s *Server) Health(ctx context.Context) control.Health {
	toks, _ := s.deps.Store.Tokens(ctx)
	return control.Health{
		RelayID: s.deps.RelayID, Region: s.deps.Region, Version: s.deps.Version,
		PublicHost: s.deps.PublicHost, BaseDomain: s.deps.BaseDomain,
		Agents: len(s.deps.Sessions.Sessions()), Routes: len(s.deps.Registry.Entries()),
		Tokens: len(toks), Usage: s.deps.Usage.Snapshot(), ReportedAt: time.Now().UTC(),
	}
}

// --- helpers ---

func toTokenInfo(t *store.Token) control.TokenInfo {
	return control.TokenInfo{
		ID: t.ID, Name: t.Name, Hostnames: t.Scope.Hostnames, TCP: t.Scope.TCP,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, RevokedAt: t.RevokedAt, LastSeenAt: t.LastSeenAt,
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
