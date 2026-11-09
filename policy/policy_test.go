package policy

import (
	"net/http"
	"testing"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		xff     http.Header
		trusted []string
		want    string
	}{
		{"no proxy, ignore xff", "203.0.113.5:1234", hdr("X-Forwarded-For", "1.2.3.4"), nil, "203.0.113.5"},
		{"trusted proxy one hop", "10.0.0.1:9", hdr("X-Forwarded-For", "203.0.113.5"), []string{"10.0.0.0/8"}, "203.0.113.5"},
		{"trusted chain", "10.0.0.1:9", hdr("X-Forwarded-For", "203.0.113.5, 10.0.0.2"), []string{"10.0.0.0/8"}, "203.0.113.5"},
		{"spoofed xff from untrusted peer", "203.0.113.5:9", hdr("X-Forwarded-For", "1.1.1.1"), []string{"10.0.0.0/8"}, "203.0.113.5"},
		{"untrusted hop stops walk", "10.0.0.1:9", hdr("X-Forwarded-For", "9.9.9.9, 8.8.8.8"), []string{"10.0.0.0/8"}, "8.8.8.8"},
		{"ipv6 peer", "[2001:db8::1]:9", nil, nil, "2001:db8::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClientIP(c.remote, c.xff, c.trusted); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func ipReq(ip string) Request { return Request{RemoteAddr: ip + ":1000"} }

func TestIPAllowDeny(t *testing.T) {
	deny := model.IPPolicy{ID: "pol_block", Rules: []model.IPRule{{Action: model.ActionDeny, CIDR: "203.0.113.0/24"}}}
	e, err := New(Options{Where: "relay"}, []model.IPPolicy{deny}, nil, map[string]Binding{"rt": {IPPolicyID: "pol_block"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate("rt", ipReq("203.0.113.9"), time.Now()); d.Allow {
		t.Error("203.0.113.9 should be denied")
	}
	if d := e.Evaluate("rt", ipReq("198.51.100.1"), time.Now()); !d.Allow {
		t.Error("198.51.100.1 should be allowed")
	}
}

func TestIPAllowlist(t *testing.T) {
	// Default deny with an allow rule (allowlist).
	allow := model.IPPolicy{ID: "pol_allow", DefaultAction: model.ActionDeny,
		Rules: []model.IPRule{{Action: model.ActionAllow, CIDR: "10.0.0.0/8"}}}
	e, _ := New(Options{}, []model.IPPolicy{allow}, nil, map[string]Binding{"rt": {IPPolicyID: "pol_allow"}})
	if d := e.Evaluate("rt", ipReq("10.1.2.3"), time.Now()); !d.Allow {
		t.Error("10.1.2.3 should be allowed by allowlist")
	}
	if d := e.Evaluate("rt", ipReq("8.8.8.8"), time.Now()); d.Allow {
		t.Error("8.8.8.8 should be denied by allowlist default")
	}
}

func TestGlobalDenyBeatsRouteAllow(t *testing.T) {
	gdeny := model.IPPolicy{ID: "g", Global: true, Rules: []model.IPRule{{Action: model.ActionDeny, CIDR: "1.2.3.4/32"}}}
	rallow := model.IPPolicy{ID: "r", Rules: []model.IPRule{{Action: model.ActionAllow, CIDR: "1.2.3.4/32"}}}
	e, _ := New(Options{}, []model.IPPolicy{gdeny, rallow}, nil, map[string]Binding{"rt": {IPPolicyID: "r"}})
	if d := e.Evaluate("rt", ipReq("1.2.3.4"), time.Now()); d.Allow {
		t.Error("global deny must beat route allow")
	} else if d.RuleID != "g" {
		t.Errorf("deny should cite global policy, got %q", d.RuleID)
	}
}

func TestRateLimitReject(t *testing.T) {
	rl := model.RateLimitRule{ID: "rl1", Keys: []string{"ip"}, Limit: 3, WindowSec: 60}
	e, _ := New(Options{}, nil, []model.RateLimitRule{rl}, map[string]Binding{"rt": {RateLimitIDs: []string{"rl1"}}})
	now := time.Now()
	allowed := 0
	for i := 0; i < 10; i++ {
		if e.Evaluate("rt", ipReq("5.5.5.5"), now).Allow {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("expected 3 allowed within limit, got %d", allowed)
	}
	d := e.Evaluate("rt", ipReq("5.5.5.5"), now)
	if d.Allow || d.Status != http.StatusTooManyRequests || d.RetryAfter < 1 {
		t.Errorf("expected 429 with Retry-After, got %+v", d)
	}
	// A different IP is unaffected.
	if !e.Evaluate("rt", ipReq("6.6.6.6"), now).Allow {
		t.Error("different IP should be allowed")
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	rl := model.RateLimitRule{ID: "rl1", Keys: []string{"ip"}, Limit: 2, WindowSec: 10}
	e, _ := New(Options{}, nil, []model.RateLimitRule{rl}, map[string]Binding{"rt": {RateLimitIDs: []string{"rl1"}}})
	now := time.Now()
	e.Evaluate("rt", ipReq("5.5.5.5"), now)
	e.Evaluate("rt", ipReq("5.5.5.5"), now)
	if e.Evaluate("rt", ipReq("5.5.5.5"), now).Allow {
		t.Error("third request in window should be denied")
	}
	// Well past two windows: fully reset.
	later := now.Add(30 * time.Second)
	if !e.Evaluate("rt", ipReq("5.5.5.5"), later).Allow {
		t.Error("request after window reset should be allowed")
	}
}

func TestHeaderRules(t *testing.T) {
	rules := []model.HeaderRule{
		{Phase: model.HeaderRequest, Op: model.HeaderSet, Name: "X-From", Value: "latch"},
		{Phase: model.HeaderRequest, Op: model.HeaderDelete, Name: "Cookie"},
		{Phase: model.HeaderResponse, Op: model.HeaderSet, Name: "X-Served", Value: "relay"},
	}
	e, _ := New(Options{}, nil, nil, map[string]Binding{"rt": {Headers: rules}})
	h := hdr("Cookie", "secret", "Host", "x")
	e.ApplyRequest("rt", h)
	if h.Get("X-From") != "latch" || h.Get("Cookie") != "" {
		t.Errorf("request headers not applied: %v", h)
	}
	resp := http.Header{}
	e.ApplyResponse("rt", resp)
	if resp.Get("X-Served") != "relay" {
		t.Errorf("response headers not applied: %v", resp)
	}
}

func TestGeoMissingFlag(t *testing.T) {
	geo := model.IPPolicy{ID: "g", Global: true, Rules: []model.IPRule{{Action: model.ActionDeny, Country: "RU"}}}
	e, _ := New(Options{GeoRequired: true}, []model.IPPolicy{geo}, nil, nil)
	if !e.GeoMissing() {
		t.Error("engine should report GeoMissing when a country rule exists but no DB")
	}
	// Without a DB, a country rule cannot match → request is allowed (fail-open
	// with a warning handled by the caller).
	if d := e.Evaluate("rt", ipReq("1.2.3.4"), time.Now()); !d.Allow {
		t.Error("country rule without DB should not deny")
	}
}

func TestExplainDeny(t *testing.T) {
	deny := model.IPPolicy{ID: "pol_block", Rules: []model.IPRule{{Action: model.ActionDeny, CIDR: "203.0.113.0/24"}}}
	e, _ := New(Options{Where: "relay"}, []model.IPPolicy{deny}, nil, map[string]Binding{"rt": {IPPolicyID: "pol_block"}})
	ex := e.Explain("rt", Request{RemoteAddr: "203.0.113.9:1000"})
	if ex.Result != "deny" || ex.RuleID != "pol_block" || ex.Where != "relay" {
		t.Errorf("explanation = %+v", ex)
	}
	if ex.ClientIP != "203.0.113.9" {
		t.Errorf("client ip = %q", ex.ClientIP)
	}
	var sawRoutePolicy bool
	for _, st := range ex.Steps {
		if st.Stage == "ip_policy:route" && st.Matched {
			sawRoutePolicy = true
		}
	}
	if !sawRoutePolicy {
		t.Errorf("expected a matched route-policy step: %+v", ex.Steps)
	}
}

func TestExplainAllow(t *testing.T) {
	e, _ := New(Options{Where: "agent"}, nil, nil, map[string]Binding{"rt": {}})
	ex := e.Explain("rt", Request{RemoteAddr: "8.8.8.8:1000"})
	if ex.Result != "allow" {
		t.Errorf("expected allow, got %+v", ex)
	}
}
