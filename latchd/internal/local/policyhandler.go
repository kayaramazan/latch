package local

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/model"
)

func init() {
	caddy.RegisterModule(LatchPolicy{})
}

// Runtime is the live policy state the latch_policy handler reads. The manager
// replaces it (atomically) whenever the config changes, so the handler always
// sees the current engine without Caddy re-provisioning.
type Runtime struct {
	Engine  *policy.Engine
	Emit    func(model.AccessEvent)
	Trusted []string
}

var active atomic.Pointer[Runtime]

// SetRuntime installs the current policy runtime for the handler.
func SetRuntime(r *Runtime) { active.Store(r) }

// LatchPolicy is a Caddy HTTP handler that applies the shared /policy engine
// (IP/Geo/rate limit, header rules) and emits a local AccessEvent per request.
type LatchPolicy struct {
	RouteID string `json:"route_id,omitempty"`
}

// CaddyModule registers the handler.
func (LatchPolicy) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.latch_policy",
		New: func() caddy.Module { return new(LatchPolicy) },
	}
}

// ServeHTTP enforces policy, then calls next (the reverse proxy).
func (h LatchPolicy) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	rt := active.Load()
	start := time.Now()
	host := hostOnly(r.Host)
	ev := model.AccessEvent{TS: start.UTC(), Source: model.SourceLocal, Hostname: host,
		Method: r.Method, Path: r.URL.Path, Where: "agent", Decision: model.DecisionAllow, RouteID: h.RouteID}

	if rt == nil || rt.Engine == nil {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		err := next.ServeHTTP(rec, r)
		ev.Status = rec.status
		ev.BytesOut = rec.bytes
		emit(rt, ev, start)
		return err
	}

	req := policy.Request{RemoteAddr: r.RemoteAddr, Header: r.Header, Method: r.Method, Path: r.URL.Path, Host: host}
	d := rt.Engine.Evaluate(h.RouteID, req, start)
	ev.ClientIP, ev.Country = d.ClientIP, d.Country

	if !d.Allow {
		status := d.Status
		if status == 0 {
			status = http.StatusForbidden
		}
		if d.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(d.RetryAfter))
		}
		if d.Drop {
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = conn.Close()
			}
			ev.Status = 0
		} else {
			http.Error(w, http.StatusText(status), status)
			ev.Status = status
		}
		ev.Decision, ev.DenyReason, ev.RuleID = model.DecisionDeny, d.DenyReason, d.RuleID
		emit(rt, ev, start)
		return nil
	}
	if d.DelayMs > 0 {
		select {
		case <-time.After(time.Duration(d.DelayMs) * time.Millisecond):
		case <-r.Context().Done():
		}
	}
	rt.Engine.ApplyRequest(h.RouteID, r.Header)

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK,
		onHeader: func(hdr http.Header) { rt.Engine.ApplyResponse(h.RouteID, hdr) }}
	err := next.ServeHTTP(rec, r)
	ev.Status = rec.status
	ev.BytesOut = rec.bytes
	ev.BytesIn = r.ContentLength
	if ev.BytesIn < 0 {
		ev.BytesIn = 0
	}
	emit(rt, ev, start)
	return err
}

func emit(rt *Runtime, ev model.AccessEvent, start time.Time) {
	ev.DurationMs = time.Since(start).Milliseconds()
	if rt != nil && rt.Emit != nil {
		rt.Emit(ev)
	}
}

// statusRecorder captures status/bytes and applies response headers once,
// just before the header is written. It forwards Flush and Hijack so SSE and
// WebSocket keep working.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
	onHeader    func(http.Header)
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	if s.onHeader != nil {
		s.onHeader(s.Header())
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		if !s.wroteHeader && s.onHeader != nil {
			s.onHeader(s.Header())
		}
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Unwrap lets Caddy's ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func hostOnly(h string) string {
	if i := strings.IndexByte(h, ':'); i >= 0 {
		return h[:i]
	}
	return h
}

// interface guards
var (
	_ caddyhttp.MiddlewareHandler = LatchPolicy{}
	_ caddy.Module                = LatchPolicy{}
)
