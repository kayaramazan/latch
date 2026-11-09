// Package policy is the shared policy engine: client-IP resolution, IP/CIDR and
// GeoIP allow/deny, sliding-window rate limiting, header rules, and an Explain
// for observability. It runs inside the relay (tunnel mode) and, from phase 6,
// inside latchd as a Caddy handler (local mode). It is pure and concurrency-safe.
package policy

import (
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

// Binding is the set of policies attached to one route.
type Binding struct {
	IPPolicyID   string
	RateLimitIDs []string
	Headers      []model.HeaderRule
}

// Options configure an Engine.
type Options struct {
	TrustedProxies []string
	Where          string // "relay" or "agent", copied into decisions/events
	GeoIP          *GeoDB // may be nil
	GeoRequired    bool   // a country rule exists but the DB is missing when true+nil
	MaxRateKeys    int
}

// Engine evaluates compiled policies against requests. Build one with New and
// replace it wholesale on PolicySync (cheap; limiter state resets on change).
type Engine struct {
	opts     Options
	trusted  []string
	ipByID   map[string]*compiledIPPolicy
	globalIP []*compiledIPPolicy
	rateByID map[string]*rateRule
	globalRL []*rateRule
	bindings map[string]Binding
	geoMiss  bool // a country rule exists but no GeoIP DB
}

type rateRule struct {
	def     model.RateLimitRule
	limiter *slidingLimiter
}

// New compiles policies into an Engine.
func New(opts Options, ipPolicies []model.IPPolicy, rateRules []model.RateLimitRule, bindings map[string]Binding) (*Engine, error) {
	e := &Engine{
		opts:     opts,
		trusted:  opts.TrustedProxies,
		ipByID:   map[string]*compiledIPPolicy{},
		rateByID: map[string]*rateRule{},
		bindings: bindings,
	}
	if e.bindings == nil {
		e.bindings = map[string]Binding{}
	}
	for _, p := range ipPolicies {
		cp, err := compileIPPolicy(p)
		if err != nil {
			return nil, err
		}
		e.ipByID[cp.id] = cp
		if cp.global {
			e.globalIP = append(e.globalIP, cp)
		}
		if cp.hasGeoRule && (opts.GeoIP == nil) {
			e.geoMiss = true
		}
	}
	for _, r := range rateRules {
		r.Normalize()
		rr := &rateRule{def: r, limiter: newSlidingLimiter(r.Limit, r.WindowSec, opts.MaxRateKeys)}
		e.rateByID[r.ID] = rr
		if r.Global {
			e.globalRL = append(e.globalRL, rr)
		}
	}
	return e, nil
}

// GeoMissing reports whether a country rule exists but no GeoIP DB is loaded.
func (e *Engine) GeoMissing() bool { return e.geoMiss }

// Request carries the attributes a decision needs.
type Request struct {
	RemoteAddr string
	Header     http.Header
	Method     string
	Path       string
	Host       string
}

// Decision is the outcome of Evaluate.
type Decision struct {
	Allow      bool
	Where      string
	ClientIP   string
	Country    string
	DenyReason string // model.Deny*
	RuleID     string
	Status     int  // HTTP status to return on deny (0 => default 403)
	RetryAfter int  // seconds, rate-limit deny
	DelayMs    int  // >0 => delay instead of reject
	Drop       bool // close the connection without a response
}

// Evaluate runs IP/Geo rules then rate limits for routeID. Header rules do not
// deny; call RequestHeaders/ResponseHeaders separately.
func (e *Engine) Evaluate(routeID string, r Request, now time.Time) Decision {
	d := Decision{Allow: true, Where: e.opts.Where}
	d.ClientIP = ClientIP(r.RemoteAddr, r.Header, e.trusted)
	addr, _ := netip.ParseAddr(d.ClientIP)

	b := e.bindings[routeID]
	var routePol *compiledIPPolicy
	if b.IPPolicyID != "" {
		routePol = e.ipByID[b.IPPolicyID]
	}

	needGeo := (routePol != nil && routePol.hasGeoRule)
	for _, g := range e.globalIP {
		if g.hasGeoRule {
			needGeo = true
		}
	}
	if needGeo && e.opts.GeoIP != nil {
		d.Country = e.opts.GeoIP.Country(addr)
	}

	// IP / Geo.
	if res := combineIP(e.globalIP, routePol, addr, d.Country); res.deny {
		d.Allow = false
		d.RuleID = res.policy.id
		d.DenyReason = model.DenyIPPolicy
		if res.isGeo {
			d.DenyReason = model.DenyGeo
		}
		if res.policy.onDeny.Mode == model.DenyDrop {
			d.Drop = true
		} else {
			d.Status = res.policy.onDeny.Status
			if d.Status == 0 {
				d.Status = http.StatusForbidden
			}
		}
		return d
	}

	// Rate limits: route-bound then global.
	var rules []*rateRule
	for _, id := range b.RateLimitIDs {
		if rr := e.rateByID[id]; rr != nil {
			rules = append(rules, rr)
		}
	}
	rules = append(rules, e.globalRL...)
	for _, rr := range rules {
		key := e.rateKey(rr.def, routeID, d.ClientIP, r)
		ok, retry := rr.limiter.Allow(key, now)
		if ok {
			continue
		}
		d.Allow = false
		d.DenyReason = model.DenyRateLimit
		d.RuleID = rr.def.ID
		if rr.def.OnExceed.Mode == model.ExceedDelay {
			d.DelayMs = rr.def.OnExceed.MaxDelayMs
			d.Allow = true // delayed, not rejected
			return d
		}
		d.Status = http.StatusTooManyRequests
		d.RetryAfter = int(retry.Seconds())
		if d.RetryAfter < 1 {
			d.RetryAfter = 1
		}
		return d
	}
	return d
}

// rateKey builds the limiter key from the rule's key fields.
func (e *Engine) rateKey(def model.RateLimitRule, routeID, clientIP string, r Request) string {
	parts := make([]string, 0, len(def.Keys)+2)
	parts = append(parts, def.ID, routeID)
	for _, k := range def.Keys {
		switch {
		case k == "ip":
			parts = append(parts, clientIP)
		case k == "path":
			parts = append(parts, r.Path)
		case strings.HasPrefix(k, "header:"):
			parts = append(parts, r.Header.Get(strings.TrimPrefix(k, "header:")))
		default:
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "|")
}

// RequestHeaders returns the request-phase header rules for a route.
func (e *Engine) RequestHeaders(routeID string) []model.HeaderRule {
	return e.bindings[routeID].Headers
}

// ApplyRequest applies request-phase header rules for routeID to h.
func (e *Engine) ApplyRequest(routeID string, h http.Header) {
	ApplyHeaderRules(h, e.bindings[routeID].Headers, model.HeaderRequest)
}

// ApplyResponse applies response-phase header rules for routeID to h.
func (e *Engine) ApplyResponse(routeID string, h http.Header) {
	ApplyHeaderRules(h, e.bindings[routeID].Headers, model.HeaderResponse)
}
