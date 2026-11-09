package policy

import (
	"fmt"
	"net/netip"

	"github.com/kayaramazan/latch/proto/model"
)

// ExplainStep is one stage of the evaluation trace.
type ExplainStep struct {
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
	Matched bool   `json:"matched"`
	Result  string `json:"result,omitempty"` // allow | deny | pass
}

// Explanation is the result of Explain: what the engine would decide for a
// sample request and why.
type Explanation struct {
	RouteID    string        `json:"routeId"`
	Where      string        `json:"where"` // relay | agent
	ClientIP   string        `json:"clientIp"`
	Country    string        `json:"country,omitempty"`
	Result     string        `json:"result"` // allow | deny
	DenyReason string        `json:"denyReason,omitempty"`
	RuleID     string        `json:"ruleId,omitempty"`
	Steps      []ExplainStep `json:"steps"`
}

// Explain traces how the engine evaluates a sample request for routeID without
// consuming rate-limit budget (rate limits are shown as configured, not run).
func (e *Engine) Explain(routeID string, r Request) Explanation {
	ex := Explanation{RouteID: routeID, Where: e.opts.Where, Result: "allow"}
	ex.ClientIP = ClientIP(r.RemoteAddr, r.Header, e.trusted)
	addr, _ := netip.ParseAddr(ex.ClientIP)
	ex.Steps = append(ex.Steps, ExplainStep{Stage: "client_ip",
		Detail: fmt.Sprintf("resolved to %s (trusted proxies: %v)", ex.ClientIP, e.trusted), Matched: true})

	b := e.bindings[routeID]
	var routePol *compiledIPPolicy
	if b.IPPolicyID != "" {
		routePol = e.ipByID[b.IPPolicyID]
	}
	needGeo := routePol != nil && routePol.hasGeoRule
	for _, g := range e.globalIP {
		if g.hasGeoRule {
			needGeo = true
		}
	}
	if needGeo {
		if e.opts.GeoIP != nil {
			ex.Country = e.opts.GeoIP.Country(addr)
			ex.Steps = append(ex.Steps, ExplainStep{Stage: "geoip", Detail: "country " + emptyDash(ex.Country), Matched: true})
		} else {
			ex.Steps = append(ex.Steps, ExplainStep{Stage: "geoip", Detail: "country rules present but no GeoIP DB; rules will not match", Matched: false})
		}
	}

	// Mirror combineIP, recording each stage.
	record := func(stage string, p *compiledIPPolicy, m *ipMatch) {
		if m == nil {
			ex.Steps = append(ex.Steps, ExplainStep{Stage: stage, Detail: policyLabel(p) + ": no rule matched", Matched: false, Result: "pass"})
			return
		}
		reason := "ip rule"
		if m.isGeo {
			reason = "country rule"
		}
		ex.Steps = append(ex.Steps, ExplainStep{Stage: stage,
			Detail:  fmt.Sprintf("%s: %s matched → %s", policyLabel(p), reason, m.action),
			Matched: true, Result: string(m.action)})
	}

	res := combineIP(e.globalIP, routePol, addr, ex.Country)
	// Produce readable steps for the policies involved.
	for _, g := range e.globalIP {
		record("ip_policy:global", g, g.match(addr, ex.Country))
	}
	if routePol != nil {
		record("ip_policy:route", routePol, routePol.match(addr, ex.Country))
	}
	if res.deny {
		ex.Result = "deny"
		ex.RuleID = res.policy.id
		ex.DenyReason = model.DenyIPPolicy
		if res.isGeo {
			ex.DenyReason = model.DenyGeo
		}
		ex.Steps = append(ex.Steps, ExplainStep{Stage: "decision",
			Detail: "denied by " + res.policy.id, Matched: true, Result: "deny"})
		return ex
	}

	// Rate limits (shown as configured; not executed).
	var rlIDs []string
	rlIDs = append(rlIDs, b.RateLimitIDs...)
	for _, rr := range e.globalRL {
		rlIDs = append(rlIDs, rr.def.ID)
	}
	for _, id := range rlIDs {
		if rr := e.rateByID[id]; rr != nil {
			ex.Steps = append(ex.Steps, ExplainStep{Stage: "rate_limit",
				Detail:  fmt.Sprintf("%s: %d / %ds by %v (evaluated live per request)", rr.def.ID, rr.def.Limit, rr.def.WindowSec, rr.def.Keys),
				Matched: false})
		}
	}
	ex.Steps = append(ex.Steps, ExplainStep{Stage: "decision", Detail: "allowed (no IP/Geo rule denied)", Matched: true, Result: "allow"})
	return ex
}

func policyLabel(p *compiledIPPolicy) string {
	if p == nil {
		return "(none)"
	}
	scope := "route"
	if p.global {
		scope = "global"
	}
	return fmt.Sprintf("%s policy %s (default %s)", scope, p.id, p.defaultAction)
}

func emptyDash(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}
