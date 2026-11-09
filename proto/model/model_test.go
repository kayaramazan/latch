package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewIDAndValidID(t *testing.T) {
	id := NewID(PrefixRoute)
	if !ValidID(id, PrefixRoute) {
		t.Fatalf("NewID produced invalid id %q", id)
	}
	if ValidID(id, PrefixRelay) {
		t.Error("prefix mismatch accepted")
	}
	if ValidID("rt_short", PrefixRoute) || ValidID("", PrefixRoute) {
		t.Error("malformed ids accepted")
	}
	if NewID(PrefixRoute) == id {
		t.Error("ids are not random")
	}
}

func TestNormalizeDefaults(t *testing.T) {
	r := Route{Name: "  app ", Target: " localhost:3000 ", Exposure: ExposureLocal, Hostnames: []string{" App.Example.COM "}}
	r.Normalize()
	if r.Kind != KindHTTP || r.PathPrefix != "/" || r.TLS != TLSAuto {
		t.Errorf("route defaults not applied: %+v", r)
	}
	if r.Hostnames[0] != "app.example.com" || r.Name != "app" || r.Target != "localhost:3000" {
		t.Errorf("not canonicalized: %+v", r)
	}
	if r.HealthCheck.Path != "/" || r.HealthCheck.IntervalSec != 10 || len(r.HealthCheck.ExpectStatus) != 2 {
		t.Errorf("health defaults: %+v", r.HealthCheck)
	}
	if r.Headers == nil || r.RateLimitIDs == nil {
		t.Error("nil slices should become empty slices for stable JSON")
	}

	tcp := Route{Kind: KindTCP, HealthCheck: HealthCheck{Path: "/x", ExpectStatus: []int{200, 200}}}
	tcp.Normalize()
	if tcp.HealthCheck.Path != "" || tcp.HealthCheck.ExpectStatus != nil {
		t.Error("tcp health check should drop http fields")
	}

	p := IPPolicy{Rules: []IPRule{{Action: ActionDeny, CIDR: "1.2.3.4"}, {Action: ActionDeny, CIDR: "::1"}, {Action: ActionAllow, Country: " tr "}}}
	p.Normalize()
	if p.DefaultAction != ActionAllow || p.OnDeny.Mode != DenyStatus || p.OnDeny.Status != 403 {
		t.Errorf("policy defaults: %+v", p)
	}
	if p.Rules[0].CIDR != "1.2.3.4/32" || p.Rules[1].CIDR != "::1/128" || p.Rules[2].Country != "TR" {
		t.Errorf("rules not canonicalized: %+v", p.Rules)
	}

	rl := RateLimitRule{OnExceed: ExceedBehavior{Mode: ExceedDelay}}
	rl.Normalize()
	if rl.Keys[0] != "ip" || rl.OnExceed.MaxDelayMs != 2000 {
		t.Errorf("rate limit defaults: %+v", rl)
	}

	var s Settings
	s.Normalize()
	s.LocalMode.HTTP3 = true // booleans cannot be defaulted by Normalize; callers start from DefaultSettings()
	if !reflect.DeepEqual(s, DefaultSettings()) {
		t.Errorf("Settings.Normalize on zero value should equal DefaultSettings: %+v", s)
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	s := validSnapshot()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(&back)
	if string(b) != string(b2) {
		t.Errorf("round trip changed JSON:\n%s\n%s", b, b2)
	}
	for _, key := range []string{`"ipPolicyId"`, `"relayId"`, `"tcpPort"`} {
		if strings.Contains(string(b), key) && !strings.Contains(string(b), key+`:"`) && !strings.Contains(string(b), key+`:2`) {
			t.Errorf("omitempty expected for %s", key)
		}
	}
}

func TestLookups(t *testing.T) {
	s := validSnapshot()
	if s.Route(s.Routes[0].ID) == nil || s.IPPolicy(s.IPPolicies[0].ID) == nil ||
		s.RateLimit(s.RateLimits[0].ID) == nil || s.Relay(s.Relays[0].ID) == nil {
		t.Error("lookup of existing objects failed")
	}
	if s.Route("rt_nope") != nil {
		t.Error("lookup of unknown id should be nil")
	}
}

// validSnapshot builds a snapshot that must pass Validate; tests mutate it.
func validSnapshot() *Snapshot {
	s := NewSnapshot()
	relay := NewRelay("vps", "relay.example.com", "tok")
	relay.BaseDomain = "relay.example.com"
	pol := NewIPPolicy("office")
	pol.Rules = []IPRule{{Action: ActionAllow, CIDR: "10.0.0.0/8"}, {Action: ActionDeny, Country: "RU"}}
	rl := NewRateLimitRule("basic", 100, 60)
	local := NewRoute("site", "127.0.0.1:8080", ExposureLocal)
	local.Hostnames = []string{"site.example.com"}
	local.IPPolicyID = pol.ID
	local.RateLimitIDs = []string{rl.ID}
	local.Headers = []HeaderRule{{Phase: HeaderResponse, Op: HeaderSet, Name: "X-Frame-Options", Value: "DENY"}}
	tun := NewRoute("app", "localhost:3000", ExposureTunnel)
	tun.Hostnames = []string{"app.relay.example.com"}
	tun.RelayID = relay.ID
	tcp := NewRoute("ssh", "127.0.0.1:22", ExposureTunnel)
	tcp.Kind = KindTCP
	tcp.RelayID = relay.ID
	tcp.Normalize()
	s.Relays = []Relay{relay}
	s.IPPolicies = []IPPolicy{pol}
	s.RateLimits = []RateLimitRule{rl}
	s.Routes = []Route{local, tun, tcp}
	s.Normalize()
	return s
}
