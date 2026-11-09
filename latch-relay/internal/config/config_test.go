package config

import (
	"path/filepath"
	"testing"
)

func TestWriteLoadRoundTrip(t *testing.T) {
	c := &Config{
		PublicHost: "relay.example.com",
		ACME:       ACME{Enabled: true, Email: "a@b.com", DNSProvider: "cloudflare", CloudflareToken: "tok"},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := c.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicHost != "relay.example.com" || got.BaseDomain != "relay.example.com" {
		t.Errorf("host/base = %q/%q", got.PublicHost, got.BaseDomain)
	}
	if got.HTTPSAddr != ":443" || got.AgentAddr != ":7443" || got.TCPPorts != "20000-20999" {
		t.Errorf("defaults not applied: %+v", got)
	}
	if !got.ACME.Enabled || got.ACME.CloudflareToken != "tok" {
		t.Errorf("acme = %+v", got.ACME)
	}
}

func TestValidateErrors(t *testing.T) {
	if (&Config{}).Validate() == nil {
		t.Error("missing public host should fail")
	}
	if (&Config{PublicHost: "r", ACME: ACME{Enabled: true}}).Validate() == nil {
		t.Error("acme without email should fail")
	}
	if (&Config{PublicHost: "r", ACME: ACME{Enabled: true, Email: "a@b", DNSProvider: "bogus"}}).Validate() == nil {
		t.Error("unknown provider should fail")
	}
	if (&Config{PublicHost: "r", ACME: ACME{Enabled: true, Email: "a@b", DNSProvider: "cloudflare"}}).Validate() == nil {
		t.Error("cloudflare without token should fail")
	}
	ok := &Config{PublicHost: "r", ACME: ACME{Enabled: true, Email: "a@b", DNSProvider: "manual"}}
	ok.applyDefaults()
	if err := ok.Validate(); err != nil {
		t.Errorf("manual provider should validate: %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := writeFile(path, "public_host: r\nbogus_field: 1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("unknown field should be rejected")
	}
}

func writeFile(path, content string) error {
	return osWriteFile(path, content)
}
