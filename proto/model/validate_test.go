package model

import (
	"strings"
	"testing"
)

func TestValidateAcceptsValidSnapshot(t *testing.T) {
	if err := Validate(validSnapshot()); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if err := Validate(NewSnapshot()); err != nil {
		t.Fatalf("empty snapshot rejected: %v", err)
	}
}

func TestValidateTable(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(s *Snapshot)
		wantPath string
		wantCode string
	}{
		{"missing name", func(s *Snapshot) { s.Routes[0].Name = "" }, "routes[0].name", CodeRequired},
		{"long name", func(s *Snapshot) { s.Routes[0].Name = strings.Repeat("x", 65) }, "routes[0].name", CodeTooLong},
		{"duplicate route name", func(s *Snapshot) { s.Routes[1].Name = "SITE" }, "routes[1].name", CodeDuplicate},
		{"bad target", func(s *Snapshot) { s.Routes[0].Target = "localhost" }, "routes[0].target", CodeInvalid},
		{"bad port", func(s *Snapshot) { s.Routes[0].Target = "localhost:70000" }, "routes[0].target", CodeInvalid},
		{"bad exposure", func(s *Snapshot) { s.Routes[0].Exposure = "cloud" }, "routes[0].exposure", CodeInvalid},
		{"tunnel without relay", func(s *Snapshot) { s.Routes[1].RelayID = "" }, "routes[1].relayId", CodeRequired},
		{"tunnel unknown relay", func(s *Snapshot) { s.Routes[1].RelayID = NewID(PrefixRelay) }, "routes[1].relayId", CodeUnknownRef},
		{"tunnel custom tls", func(s *Snapshot) {
			s.Routes[1].TLS = TLSCustom
			s.Routes[1].TLSCertFile = "a"
			s.Routes[1].TLSKeyFile = "b"
		}, "routes[1].tls", CodeNotAllowed},
		{"local with relay", func(s *Snapshot) { s.Routes[0].RelayID = s.Relays[0].ID }, "routes[0].relayId", CodeNotAllowed},
		{"tcp local", func(s *Snapshot) { s.Routes[2].Exposure = ExposureLocal; s.Routes[2].RelayID = "" }, "routes[2].kind", CodeNotAllowed},
		{"tcp with hostnames", func(s *Snapshot) { s.Routes[2].Hostnames = []string{"x.example.com"} }, "routes[2].hostnames", CodeNotAllowed},
		{"tcp bad port", func(s *Snapshot) { s.Routes[2].TCPPort = 99999 }, "routes[2].tcpPort", CodeInvalidPort},
		{"http tcpPort", func(s *Snapshot) { s.Routes[0].TCPPort = 20000 }, "routes[0].tcpPort", CodeNotAllowed},
		{"http proxyProtocol", func(s *Snapshot) { s.Routes[0].ProxyProtocol = true }, "routes[0].proxyProtocol", CodeNotAllowed},
		{"no hostnames", func(s *Snapshot) { s.Routes[0].Hostnames = nil }, "routes[0].hostnames", CodeRequired},
		{"wildcard hostname", func(s *Snapshot) { s.Routes[0].Hostnames = []string{"*.example.com"} }, "routes[0].hostnames[0]", CodeInvalidHost},
		{"ip hostname", func(s *Snapshot) { s.Routes[0].Hostnames = []string{"1.2.3.4"} }, "routes[0].hostnames[0]", CodeInvalidHost},
		{"single label", func(s *Snapshot) { s.Routes[0].Hostnames = []string{"localhost"} }, "routes[0].hostnames[0]", CodeInvalidHost},
		{"bad label chars", func(s *Snapshot) { s.Routes[0].Hostnames = []string{"my_app.example.com"} }, "routes[0].hostnames[0]", CodeInvalidHost},
		{"repeated hostname", func(s *Snapshot) { s.Routes[0].Hostnames = []string{"a.example.com", "a.example.com"} }, "routes[0].hostnames[1]", CodeDuplicate},
		{"hostname collision across routes", func(s *Snapshot) { s.Routes[1].Hostnames = []string{"site.example.com"} }, "routes[1].hostnames[0]", CodeConflict},
		{"same hostname different prefix ok", func(s *Snapshot) {
			s.Routes[1].Hostnames = []string{"site.example.com"}
			s.Routes[1].PathPrefix = "/api"
		}, "", ""},
		{"bad prefix", func(s *Snapshot) { s.Routes[0].PathPrefix = "api" }, "routes[0].pathPrefix", CodeInvalid},
		{"custom tls missing files", func(s *Snapshot) { s.Routes[0].TLS = TLSCustom }, "routes[0].tlsCertFile", CodeRequired},
		{"tls off with redirect", func(s *Snapshot) { s.Routes[0].TLS = TLSOff }, "routes[0].httpsRedirect", CodeNotAllowed},
		{"cert file without custom", func(s *Snapshot) { s.Routes[0].TLSCertFile = "x" }, "routes[0].tlsCertFile", CodeNotAllowed},
		{"too many headers", func(s *Snapshot) {
			for i := 0; i < 33; i++ {
				s.Routes[0].Headers = append(s.Routes[0].Headers, HeaderRule{Phase: HeaderRequest, Op: HeaderDelete, Name: "X-A"})
			}
		}, "routes[0].headers", CodeTooMany},
		{"header set without value", func(s *Snapshot) { s.Routes[0].Headers[0].Value = "" }, "routes[0].headers[0].value", CodeRequired},
		{"hop-by-hop header", func(s *Snapshot) { s.Routes[0].Headers[0].Name = "Connection" }, "routes[0].headers[0].name", CodeNotAllowed},
		{"header name invalid", func(s *Snapshot) { s.Routes[0].Headers[0].Name = "X Bad" }, "routes[0].headers[0].name", CodeInvalid},
		{"header crlf", func(s *Snapshot) { s.Routes[0].Headers[0].Value = "a\r\nb" }, "routes[0].headers[0].value", CodeInvalid},
		{"health timeout >= interval", func(s *Snapshot) { s.Routes[0].HealthCheck.TimeoutSec = 10 }, "routes[0].healthCheck.timeoutSec", CodeOutOfRange},
		{"health bad expect", func(s *Snapshot) { s.Routes[0].HealthCheck.ExpectStatus = []int{200} }, "routes[0].healthCheck.expectStatus", CodeInvalid},
		{"unknown policy", func(s *Snapshot) { s.Routes[0].IPPolicyID = NewID(PrefixIPPolicy) }, "routes[0].ipPolicyId", CodeUnknownRef},
		{"unknown rate limit", func(s *Snapshot) { s.Routes[0].RateLimitIDs = []string{NewID(PrefixRateLimit)} }, "routes[0].rateLimitIds[0]", CodeUnknownRef},
		{"repeated rate limit", func(s *Snapshot) { s.Routes[0].RateLimitIDs = []string{s.RateLimits[0].ID, s.RateLimits[0].ID} }, "routes[0].rateLimitIds[1]", CodeDuplicate},
		{"duplicate route id", func(s *Snapshot) { s.Routes[1].ID = s.Routes[0].ID }, "routes[1].id", CodeDuplicate},
		{"malformed route id", func(s *Snapshot) { s.Routes[0].ID = "route-1" }, "routes[0].id", CodeInvalid},

		{"policy bad default", func(s *Snapshot) { s.IPPolicies[0].DefaultAction = "maybe" }, "ipPolicies[0].defaultAction", CodeInvalid},
		{"policy bad status", func(s *Snapshot) { s.IPPolicies[0].OnDeny.Status = 500 }, "ipPolicies[0].onDeny.status", CodeInvalid},
		{"policy drop with status", func(s *Snapshot) { s.IPPolicies[0].OnDeny = DenyBehavior{Mode: DenyDrop, Status: 403} }, "ipPolicies[0].onDeny.status", CodeNotAllowed},
		{"policy rule both matchers", func(s *Snapshot) { s.IPPolicies[0].Rules[0].Country = "TR" }, "ipPolicies[0].rules[0]", CodeInvalid},
		{"policy rule no matcher", func(s *Snapshot) { s.IPPolicies[0].Rules[0].CIDR = "" }, "ipPolicies[0].rules[0]", CodeRequired},
		{"policy bad cidr", func(s *Snapshot) { s.IPPolicies[0].Rules[0].CIDR = "10.0.0.0/33" }, "ipPolicies[0].rules[0].cidr", CodeInvalidCIDR},
		{"policy bad country", func(s *Snapshot) { s.IPPolicies[0].Rules[1].Country = "TUR" }, "ipPolicies[0].rules[1].country", CodeInvalid},
		{"policy duplicate id", func(s *Snapshot) { s.IPPolicies = append(s.IPPolicies, s.IPPolicies[0]) }, "ipPolicies[1].id", CodeDuplicate},

		{"rate limit bad key", func(s *Snapshot) { s.RateLimits[0].Keys = []string{"cookie"} }, "rateLimits[0].keys[0]", CodeInvalid},
		{"rate limit header key ok", func(s *Snapshot) { s.RateLimits[0].Keys = []string{"ip", "header:X-Api-Key"} }, "", ""},
		{"rate limit too many keys", func(s *Snapshot) { s.RateLimits[0].Keys = []string{"ip", "path", "header:A", "header:B"} }, "rateLimits[0].keys", CodeOutOfRange},
		{"rate limit zero", func(s *Snapshot) { s.RateLimits[0].Limit = 0 }, "rateLimits[0].limit", CodeOutOfRange},
		{"rate limit window", func(s *Snapshot) { s.RateLimits[0].WindowSec = 7200 }, "rateLimits[0].windowSec", CodeOutOfRange},
		{"rate limit reject with delay", func(s *Snapshot) { s.RateLimits[0].OnExceed.MaxDelayMs = 10 }, "rateLimits[0].onExceed.maxDelayMs", CodeNotAllowed},

		{"relay no host", func(s *Snapshot) { s.Relays[0].Host = "" }, "relays[0].host", CodeRequired},
		{"relay ip host ok", func(s *Snapshot) { s.Relays[0].Host = "203.0.113.5" }, "", ""},
		{"relay bad port", func(s *Snapshot) { s.Relays[0].Port = 0 }, "relays[0].port", CodeInvalidPort},
		{"relay no token", func(s *Snapshot) { s.Relays[0].Token = "" }, "relays[0].token", CodeRequired},
		{"relay bad ca", func(s *Snapshot) { s.Relays[0].CAPEM = "garbage" }, "relays[0].caPem", CodeInvalid},

		{"settings bad listen", func(s *Snapshot) { s.Settings.Listen = "4040" }, "settings.listen", CodeInvalid},
		{"settings public bind no auth", func(s *Snapshot) { s.Settings.Listen = "0.0.0.0:4040" }, "settings.auth.mode", CodeInsecureBind},
		{"settings public bind with auth ok", func(s *Snapshot) {
			s.Settings.Listen = "0.0.0.0:4040"
			s.Settings.Auth = AuthSettings{Mode: AuthPassword, PasswordHash: "$argon2id$x"}
		}, "", ""},
		{"settings password without hash", func(s *Snapshot) { s.Settings.Auth.Mode = AuthPassword }, "settings.auth.passwordHash", CodeRequired},
		{"settings bad trusted proxy", func(s *Snapshot) { s.Settings.TrustedProxies = []string{"nope"} }, "settings.trustedProxies[0]", CodeInvalidCIDR},
		{"settings same ports", func(s *Snapshot) { s.Settings.LocalMode.HTTPPort = 443 }, "settings.localMode.httpsPort", CodeConflict},
		{"settings bad ca", func(s *Snapshot) { s.Settings.LocalMode.ACMECA = "buypass" }, "settings.localMode.acmeCa", CodeInvalid},
		{"settings bad email", func(s *Snapshot) { s.Settings.LocalMode.ACMEEmail = "not-an-email" }, "settings.localMode.acmeEmail", CodeInvalid},
		{"settings retention", func(s *Snapshot) { s.Settings.Events.RetentionDays = 0 }, "settings.events.retentionDays", CodeOutOfRange},
		{"settings log level", func(s *Snapshot) { s.Settings.Log.Level = "verbose" }, "settings.log.level", CodeInvalid},
		{"settings language", func(s *Snapshot) { s.Settings.Language = "1" }, "settings.language", CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSnapshot()
			tc.mutate(s)
			err := Validate(s)
			if tc.wantPath == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error at %s", tc.wantPath)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error is %T, want *ValidationError", err)
			}
			for _, f := range ve.Fields {
				if f.Path == tc.wantPath && f.Code == tc.wantCode {
					return
				}
			}
			t.Fatalf("no error at %s/%s; got %v", tc.wantPath, tc.wantCode, ve.Fields)
		})
	}
}

func TestValidationErrorsSorted(t *testing.T) {
	s := validSnapshot()
	s.Routes[1].Name = ""
	s.Routes[0].Name = ""
	s.Settings.Log.Level = "x"
	err := Validate(s)
	ve := err.(*ValidationError)
	if len(ve.Fields) != 3 || ve.Fields[0].Path != "routes[0].name" || ve.Fields[2].Path != "settings.log.level" {
		t.Errorf("unexpected order: %v", ve.Fields)
	}
	if !strings.Contains(err.Error(), "3 errors") {
		t.Errorf("message: %s", err.Error())
	}
}
