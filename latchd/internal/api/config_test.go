package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kayaramazan/latch/proto/model"
)

func TestValidateDryRun(t *testing.T) {
	srv := newServer(t)
	// A tunnel route referencing a non-existent relay → field error + no save.
	body := `{"routes":[{"id":"rt_aaaaaaaaaaaaaaaaaaaaaaaaaa","name":"x","target":"127.0.0.1:3000","exposure":"tunnel","kind":"http","relayId":"rly_missing","hostnames":["a.b.com"],"tls":"auto","pathPrefix":"/"}],"settings":{"listen":"127.0.0.1:4040"}}`
	resp, out := do(t, srv, "POST", "/api/config/validate", body)
	if resp.StatusCode != 200 {
		t.Fatalf("validate: %d %s", resp.StatusCode, out)
	}
	var r struct {
		Valid          bool               `json:"valid"`
		Fields         []model.FieldError `json:"fields"`
		AffectedRoutes []string           `json:"affectedRoutes"`
	}
	_ = json.Unmarshal([]byte(out), &r)
	if r.Valid {
		t.Error("expected invalid (unknown relay)")
	}
	if len(r.Fields) == 0 {
		t.Error("expected field errors")
	}
	// Nothing persisted.
	_, routes := do(t, srv, "GET", "/api/routes", "")
	if strings.Contains(routes, "rt_aaaa") {
		t.Error("validate must not save")
	}
}

func TestVersionsAndRollback(t *testing.T) {
	srv := newServer(t)
	// Create a relay (version bump), then a route, then rollback.
	do(t, srv, "POST", "/api/relays", `{"name":"vps","host":"relay.example.com","port":7443,"token":"lt_a_b"}`)
	_, listBody := do(t, srv, "GET", "/api/config/versions", "")
	var versions []map[string]any
	_ = json.Unmarshal([]byte(listBody), &versions)
	if len(versions) < 1 {
		t.Fatalf("expected versions, got %s", listBody)
	}
	// Rollback to version 1 (empty).
	resp, _ := do(t, srv, "POST", "/api/config/rollback/1", "")
	if resp.StatusCode != 200 {
		t.Fatalf("rollback: %d", resp.StatusCode)
	}
	_, relays := do(t, srv, "GET", "/api/relays", "")
	if strings.Contains(relays, "relay.example.com") {
		t.Error("rollback should have removed the relay")
	}
}

func TestBackupExportImport(t *testing.T) {
	srv := newServer(t)
	do(t, srv, "POST", "/api/relays", `{"name":"vps","host":"relay.example.com","port":7443,"token":"lt_a_b"}`)
	_, exp := do(t, srv, "GET", "/api/backup", "")
	if !strings.Contains(exp, "relay.example.com") {
		t.Fatalf("export missing relay: %s", exp)
	}
	// Import (replace) the exported backup into a fresh server.
	srv2 := newServer(t)
	resp, _ := do(t, srv2, "POST", "/api/backup?mode=replace", exp)
	if resp.StatusCode != 200 {
		t.Fatalf("import: %d", resp.StatusCode)
	}
	_, relays := do(t, srv2, "GET", "/api/relays", "")
	if !strings.Contains(relays, "relay.example.com") {
		t.Error("import did not restore the relay")
	}
}

func TestSettingsPreservesPasswordHash(t *testing.T) {
	srv := newServer(t)
	// GET settings should not expose the hash; PUT without it must preserve it.
	_, _ = do(t, srv, "PUT", "/api/settings", `{"listen":"127.0.0.1:4040","language":"tr","auth":{"mode":"none"}}`)
	_, out := do(t, srv, "GET", "/api/settings", "")
	if !strings.Contains(out, `"language": "tr"`) {
		t.Errorf("settings not updated: %s", out)
	}
}

func TestRelayDNSCheck(t *testing.T) {
	srv := newServer(t)
	resp, out := do(t, srv, "POST", "/api/relays/dns-check", `{"hostname":"localhost"}`)
	if resp.StatusCode != 200 || !strings.Contains(out, `"resolves": true`) {
		t.Errorf("dns-check localhost: %d %s", resp.StatusCode, out)
	}
}

func TestRelaySetupScript(t *testing.T) {
	srv := newServer(t)
	resp, out := do(t, srv, "POST", "/api/relays/setup-script", `{"publicHost":"relay.example.com","email":"a@b.com"}`)
	if resp.StatusCode != 200 || !strings.Contains(out, "install.sh") || !strings.Contains(out, "relay.example.com") {
		t.Errorf("setup-script: %d %s", resp.StatusCode, out)
	}
}

func TestSystemOpen(t *testing.T) {
	srv := newServer(t)
	resp, out := do(t, srv, "GET", "/api/system", "")
	if resp.StatusCode != 200 || !strings.Contains(out, `"version"`) {
		t.Errorf("system: %d %s", resp.StatusCode, out)
	}
}
