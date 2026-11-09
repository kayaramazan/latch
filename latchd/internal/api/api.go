// Package api serves latchd's local management REST API. In phase 3 it binds
// to loopback only and has no authentication; phase 8 adds auth and WebSocket.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/latchd/internal/tunnel"
	"github.com/kayaramazan/latch/proto/model"
)

// StatusProvider reports live tunnel state and events (tunnel.Manager).
type StatusProvider interface {
	Status() []tunnel.ClientStatus
	Events() *tunnel.EventBuffer
}

// Server is the management HTTP API.
type Server struct {
	store   *store.Store
	tun     StatusProvider
	log     *slog.Logger
	version string
	mux     *http.ServeMux
	health  func() map[string]string
}

// SetHealth installs an upstream-health provider (local mode).
func (s *Server) SetHealth(fn func() map[string]string) { s.health = fn }

// New builds the API server.
func New(st *store.Store, tun StatusProvider, version string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{store: st, tun: tun, log: log, version: version, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler exposes the mux (for tests).
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/status", s.getStatus)
	s.mux.HandleFunc("GET /api/events", s.getEvents)
	s.mux.HandleFunc("GET /api/events/stream", s.streamEvents)
	s.mux.HandleFunc("GET /api/routes", s.listRoutes)
	s.mux.HandleFunc("POST /api/routes", s.createRoute)
	s.mux.HandleFunc("GET /api/routes/{id}", s.getRoute)
	s.mux.HandleFunc("PUT /api/routes/{id}", s.updateRoute)
	s.mux.HandleFunc("DELETE /api/routes/{id}", s.deleteRoute)
	s.mux.HandleFunc("GET /api/ip-policies", s.listIPPolicies)
	s.mux.HandleFunc("POST /api/ip-policies", s.createIPPolicy)
	s.mux.HandleFunc("DELETE /api/ip-policies/{id}", s.deleteIPPolicy)
	s.mux.HandleFunc("GET /api/rate-limits", s.listRateLimits)
	s.mux.HandleFunc("POST /api/rate-limits", s.createRateLimit)
	s.mux.HandleFunc("DELETE /api/rate-limits/{id}", s.deleteRateLimit)
	s.mux.HandleFunc("GET /api/relays", s.listRelays)
	s.mux.HandleFunc("POST /api/relays", s.createRelay)
	s.mux.HandleFunc("GET /api/relays/{id}", s.getRelay)
	s.mux.HandleFunc("PUT /api/relays/{id}", s.updateRelay)
	s.mux.HandleFunc("DELETE /api/relays/{id}", s.deleteRelay)
}

// ListenAndServe binds addr (which must be loopback while auth is disabled)
// and serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if err := requireLoopback(addr); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", addr, err)
	}
	s.log.Info("management API listening", "addr", ln.Addr().String())
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// requireLoopback refuses non-loopback binds while there is no authentication.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("api: invalid listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("api: refusing to bind %q: the management API has no authentication yet and must stay on loopback", addr)
	}
	return nil
}

// --- handlers ---

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	n := 100
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	writeJSON(w, http.StatusOK, s.tun.Events().Recent(n))
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch, cancel := s.tun.Events().Subscribe()
	defer cancel()
	enc := json.NewEncoder(w)
	// Replay a small tail first.
	for _, ev := range s.tun.Events().Recent(20) {
		_, _ = w.Write([]byte("data: "))
		_ = enc.Encode(ev)
		_, _ = w.Write([]byte("\n"))
	}
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte("data: "))
			_ = enc.Encode(ev)
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request) {
	_, version := s.store.Current()
	resp := map[string]any{
		"version":       s.version,
		"configVersion": version,
		"relays":        s.tun.Status(),
	}
	if s.health != nil {
		resp["health"] = s.health()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listRoutes(w http.ResponseWriter, _ *http.Request) {
	snap, _ := s.store.Current()
	writeJSON(w, http.StatusOK, snap.Routes)
}

func (s *Server) getRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, _ := s.store.Current()
	for _, rt := range snap.Routes {
		if rt.ID == id {
			writeJSON(w, http.StatusOK, rt)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "route not found")
}

type routeInput struct {
	Name         *string             `json:"name"`
	Target       *string             `json:"target"`
	Exposure     *model.Exposure     `json:"exposure"`
	Kind         *model.RouteKind    `json:"kind"`
	RelayID      *string             `json:"relayId"`
	Hostnames    *[]string           `json:"hostnames"`
	PathPrefix   *string             `json:"pathPrefix"`
	TCPPort      *int                `json:"tcpPort"`
	Enabled      *bool               `json:"enabled"`
	IPPolicyID   *string             `json:"ipPolicyId"`
	RateLimitIDs *[]string           `json:"rateLimitIds"`
	Headers      *[]model.HeaderRule `json:"headers"`
}

func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var in routeInput
	if !decode(w, r, &in) {
		return
	}
	if in.Name == nil || in.Target == nil {
		writeErr(w, http.StatusBadRequest, "name and target are required")
		return
	}
	exposure := model.ExposureTunnel
	if in.Exposure != nil {
		exposure = *in.Exposure
	}
	rt := model.NewRoute(*in.Name, *in.Target, exposure)
	rt.Kind = model.KindHTTP
	applyRouteInput(&rt, &in)
	res, err := s.store.Update(r.Context(), "api", "create route "+rt.Name, func(snap *model.Snapshot) error {
		snap.Routes = append(snap.Routes, rt)
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, findRoute(res.Snapshot, rt.ID))
}

func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in routeInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.store.Update(r.Context(), "api", "update route "+id, func(snap *model.Snapshot) error {
		for i := range snap.Routes {
			if snap.Routes[i].ID == id {
				applyRouteInput(&snap.Routes[i], &in)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, findRoute(res.Snapshot, id))
}

func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.store.Update(r.Context(), "api", "delete route "+id, func(snap *model.Snapshot) error {
		out := snap.Routes[:0]
		found := false
		for _, rt := range snap.Routes {
			if rt.ID == id {
				found = true
				continue
			}
			out = append(out, rt)
		}
		if !found {
			return errNotFound
		}
		snap.Routes = out
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type relayInput struct {
	Name       *string `json:"name"`
	Host       *string `json:"host"`
	Port       *int    `json:"port"`
	Token      *string `json:"token"`
	CAPEM      *string `json:"caPem"`
	BaseDomain *string `json:"baseDomain"`
}

func (s *Server) listRelays(w http.ResponseWriter, _ *http.Request) {
	snap, _ := s.store.Current()
	writeJSON(w, http.StatusOK, snap.Relays)
}

func (s *Server) getRelay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, _ := s.store.Current()
	for _, rl := range snap.Relays {
		if rl.ID == id {
			writeJSON(w, http.StatusOK, rl)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "relay not found")
}

func (s *Server) createRelay(w http.ResponseWriter, r *http.Request) {
	var in relayInput
	if !decode(w, r, &in) {
		return
	}
	if in.Name == nil || in.Host == nil || in.Token == nil {
		writeErr(w, http.StatusBadRequest, "name, host and token are required")
		return
	}
	rl := model.NewRelay(*in.Name, *in.Host, *in.Token)
	applyRelayInput(&rl, &in)
	res, err := s.store.Update(r.Context(), "api", "add relay "+rl.Name, func(snap *model.Snapshot) error {
		snap.Relays = append(snap.Relays, rl)
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, findRelay(res.Snapshot, rl.ID))
}

func (s *Server) updateRelay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in relayInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.store.Update(r.Context(), "api", "update relay "+id, func(snap *model.Snapshot) error {
		for i := range snap.Relays {
			if snap.Relays[i].ID == id {
				applyRelayInput(&snap.Relays[i], &in)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, findRelay(res.Snapshot, id))
}

func (s *Server) deleteRelay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.store.Update(r.Context(), "api", "delete relay "+id, func(snap *model.Snapshot) error {
		out := snap.Relays[:0]
		found := false
		for _, rl := range snap.Relays {
			if rl.ID == id {
				found = true
				continue
			}
			out = append(out, rl)
		}
		if !found {
			return errNotFound
		}
		snap.Relays = out
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listIPPolicies(w http.ResponseWriter, _ *http.Request) {
	snap, _ := s.store.Current()
	writeJSON(w, http.StatusOK, snap.IPPolicies)
}

func (s *Server) createIPPolicy(w http.ResponseWriter, r *http.Request) {
	var p model.IPPolicy
	if !decode(w, r, &p) {
		return
	}
	if p.ID == "" {
		p.ID = model.NewID(model.PrefixIPPolicy)
	}
	res, err := s.store.Update(r.Context(), "api", "create ip policy "+p.Name, func(snap *model.Snapshot) error {
		snap.IPPolicies = append(snap.IPPolicies, p)
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for i := range res.Snapshot.IPPolicies {
		if res.Snapshot.IPPolicies[i].ID == p.ID {
			writeJSON(w, http.StatusCreated, res.Snapshot.IPPolicies[i])
			return
		}
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) deleteIPPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.store.Update(r.Context(), "api", "delete ip policy "+id, func(snap *model.Snapshot) error {
		out := snap.IPPolicies[:0]
		found := false
		for _, p := range snap.IPPolicies {
			if p.ID == id {
				found = true
				continue
			}
			out = append(out, p)
		}
		if !found {
			return errNotFound
		}
		snap.IPPolicies = out
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRateLimits(w http.ResponseWriter, _ *http.Request) {
	snap, _ := s.store.Current()
	writeJSON(w, http.StatusOK, snap.RateLimits)
}

func (s *Server) createRateLimit(w http.ResponseWriter, r *http.Request) {
	var rl model.RateLimitRule
	if !decode(w, r, &rl) {
		return
	}
	if rl.ID == "" {
		rl.ID = model.NewID(model.PrefixRateLimit)
	}
	res, err := s.store.Update(r.Context(), "api", "create rate limit "+rl.Name, func(snap *model.Snapshot) error {
		snap.RateLimits = append(snap.RateLimits, rl)
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for i := range res.Snapshot.RateLimits {
		if res.Snapshot.RateLimits[i].ID == rl.ID {
			writeJSON(w, http.StatusCreated, res.Snapshot.RateLimits[i])
			return
		}
	}
	writeJSON(w, http.StatusCreated, rl)
}

func (s *Server) deleteRateLimit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.store.Update(r.Context(), "api", "delete rate limit "+id, func(snap *model.Snapshot) error {
		out := snap.RateLimits[:0]
		found := false
		for _, rl := range snap.RateLimits {
			if rl.ID == id {
				found = true
				continue
			}
			out = append(out, rl)
		}
		if !found {
			return errNotFound
		}
		snap.RateLimits = out
		return nil
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

var errNotFound = errors.New("not found")

func applyRouteInput(rt *model.Route, in *routeInput) {
	if in.Name != nil {
		rt.Name = *in.Name
	}
	if in.Target != nil {
		rt.Target = *in.Target
	}
	if in.Exposure != nil {
		rt.Exposure = *in.Exposure
	}
	if in.Kind != nil {
		rt.Kind = *in.Kind
	}
	if in.RelayID != nil {
		rt.RelayID = *in.RelayID
	}
	if in.Hostnames != nil {
		rt.Hostnames = *in.Hostnames
	}
	if in.PathPrefix != nil {
		rt.PathPrefix = *in.PathPrefix
	}
	if in.TCPPort != nil {
		rt.TCPPort = *in.TCPPort
	}
	if in.Enabled != nil {
		rt.Enabled = *in.Enabled
	}
	if in.IPPolicyID != nil {
		rt.IPPolicyID = *in.IPPolicyID
	}
	if in.RateLimitIDs != nil {
		rt.RateLimitIDs = *in.RateLimitIDs
	}
	if in.Headers != nil {
		rt.Headers = *in.Headers
	}
}

func applyRelayInput(rl *model.Relay, in *relayInput) {
	if in.Name != nil {
		rl.Name = *in.Name
	}
	if in.Host != nil {
		rl.Host = *in.Host
	}
	if in.Port != nil {
		rl.Port = *in.Port
	}
	if in.Token != nil {
		rl.Token = *in.Token
	}
	if in.CAPEM != nil {
		rl.CAPEM = *in.CAPEM
	}
	if in.BaseDomain != nil {
		rl.BaseDomain = *in.BaseDomain
	}
}

func findRoute(snap *model.Snapshot, id string) *model.Route {
	for i := range snap.Routes {
		if snap.Routes[i].ID == id {
			return &snap.Routes[i]
		}
	}
	return nil
}

func findRelay(snap *model.Snapshot, id string) *model.Relay {
	for i := range snap.Relays {
		if snap.Relays[i].ID == id {
			return &snap.Relays[i]
		}
	}
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeStoreErr(w http.ResponseWriter, err error) {
	var ve *model.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "validation failed", "fields": ve.Fields})
	case errors.Is(err, errNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrNoChanges):
		writeErr(w, http.StatusConflict, "no changes")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
