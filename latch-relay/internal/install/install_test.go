package install

import (
	"strings"
	"testing"
)

func TestAnswersConfig(t *testing.T) {
	a := Answers{PublicHost: "relay.example.com", Email: "admin@example.com",
		DNSProvider: "cloudflare", Cloudflare: "cf-token", PublicIP: "203.0.113.10"}
	c := a.Config()
	if c.BaseDomain != "relay.example.com" {
		t.Errorf("base domain should default to public host, got %q", c.BaseDomain)
	}
	if !c.ACME.Enabled || c.ACME.Email != "admin@example.com" || c.ACME.CloudflareToken != "cf-token" {
		t.Errorf("acme = %+v", c.ACME)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("generated config should validate: %v", err)
	}
}

func TestAnswersValidate(t *testing.T) {
	if err := (Answers{Email: "a@b.com"}).Validate(); err == nil {
		t.Error("missing public host should fail")
	}
	if err := (Answers{PublicHost: "r.example.com"}).Validate(); err == nil {
		t.Error("missing email should fail")
	}
	if err := (Answers{PublicHost: "r.example.com", Email: "a@b.com", DNSProvider: "cloudflare"}).Validate(); err == nil {
		t.Error("cloudflare without token should fail")
	}
	if err := (Answers{PublicHost: "r.example.com", Email: "a@b.com", DNSProvider: "manual"}).Validate(); err != nil {
		t.Errorf("manual provider should be valid: %v", err)
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	u := RenderSystemdUnit(SystemdOptions{})
	for _, want := range []string{
		"ExecStart=/usr/local/bin/latch-relay run --config /etc/latch-relay/config.yaml",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"User=latch-relay",
		"WantedBy=multi-user.target",
		"NoNewPrivileges=true",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q\n%s", want, u)
		}
	}
}
