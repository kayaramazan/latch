package plane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kayaramazan/latch/proto/control"
)

func serve(t *testing.T, regToken string) *httptest.Server {
	t.Helper()
	p := New(Options{RegToken: regToken})
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestHeartbeatAuthAndInventory(t *testing.T) {
	srv := serve(t, "sekret")
	hb := control.Heartbeat{RelayID: "relay-eu-1", Region: "eu", Version: "test",
		ControlURL: "127.0.0.1:7444", Health: control.Health{RelayID: "relay-eu-1", Agents: 2}}
	body, _ := json.Marshal(hb)

	// Without the bearer token → 401.
	resp, _ := http.Post(srv.URL+"/relays/heartbeat", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With the token → recorded.
	req, _ := http.NewRequest("POST", srv.URL+"/relays/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sekret")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat: %d", resp.StatusCode)
	}

	resp, _ = http.Get(srv.URL + "/relays")
	var fleet []RelayRecord
	_ = json.NewDecoder(resp.Body).Decode(&fleet)
	if len(fleet) != 1 || fleet[0].RelayID != "relay-eu-1" || fleet[0].Health.Agents != 2 {
		t.Fatalf("inventory = %+v", fleet)
	}
	// ControlURL normalized to https://.
	if fleet[0].ControlURL != "https://127.0.0.1:7444" {
		t.Errorf("controlUrl = %q", fleet[0].ControlURL)
	}
}

func TestCreateTokenUnknownRelay(t *testing.T) {
	srv := serve(t, "")
	resp, _ := http.Post(srv.URL+"/relays/nope/tokens", "application/json", bytes.NewReader([]byte(`{"name":"x"}`)))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown relay, got %d", resp.StatusCode)
	}
}
