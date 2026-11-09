package local

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kayaramazan/latch/proto/model"
)

func localSnap() *model.Snapshot {
	rt := model.NewRoute("site", "127.0.0.1:8080", model.ExposureLocal)
	rt.ID = "rt_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	rt.Hostnames = []string{"site.example.com"}
	rt.Headers = []model.HeaderRule{{Phase: model.HeaderResponse, Op: model.HeaderSet, Name: "X-Served", Value: "latch"}}
	snap := &model.Snapshot{Routes: []model.Route{rt}, Settings: model.DefaultSettings()}
	snap.Normalize()
	return snap
}

func TestGenerateStructure(t *testing.T) {
	snap := localSnap()
	b, err := Generate(snap, Options{HTTPPort: 80, HTTPSPort: 443, Issuer: IssuerInternal, StorageDir: "/data"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"site.example.com"`,
		`"handler": "latch_policy"`,
		`"route_id": "rt_aaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`"handler": "reverse_proxy"`,
		`"dial": "127.0.0.1:8080"`,
		`"module": "internal"`,
		`"install_trust": false`,
		`"root": "/data"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("generated config missing %q\n%s", want, s)
		}
	}
}

func TestGenerateACMEIssuer(t *testing.T) {
	snap := localSnap()
	b, _ := Generate(snap, Options{Issuer: IssuerACME, ACMEEmail: "a@b.com"})
	s := string(b)
	if !strings.Contains(s, `"module": "acme"`) || !strings.Contains(s, `"email": "a@b.com"`) {
		t.Errorf("expected ACME issuer with email:\n%s", s)
	}
}

func TestGeneratePathAndHealth(t *testing.T) {
	rt := model.NewRoute("api", "127.0.0.1:9000", model.ExposureLocal)
	rt.ID = "rt_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	rt.Hostnames = []string{"api.example.com"}
	rt.PathPrefix = "/v1"
	rt.HealthCheck = model.HealthCheck{Enabled: true, Path: "/healthz", IntervalSec: 10, TimeoutSec: 2}
	snap := &model.Snapshot{Routes: []model.Route{rt}, Settings: model.DefaultSettings()}
	snap.Normalize()
	b, _ := Generate(snap, Options{Issuer: IssuerInternal})
	s := string(b)
	for _, want := range []string{`"/v1*"`, `"health_checks"`, `"path": "/healthz"`, `"interval": "10s"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q\n%s", want, s)
		}
	}
}

func TestGenerateEmptyWhenNoLocalRoutes(t *testing.T) {
	snap := &model.Snapshot{Settings: model.DefaultSettings()}
	snap.Normalize()
	b, err := Generate(snap, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	apps := cfg["apps"].(map[string]any)
	httpApp := apps["http"].(map[string]any)
	if _, ok := httpApp["servers"]; ok {
		t.Error("no local routes should produce no servers")
	}
}
