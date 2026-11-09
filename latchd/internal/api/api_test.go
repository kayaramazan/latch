package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/latchd/internal/tunnel"
	"github.com/kayaramazan/latch/proto/model"
)

type fakeStatus struct{}

func (fakeStatus) Status() []tunnel.ClientStatus {
	return []tunnel.ClientStatus{{RelayID: "rly_x", State: tunnel.StateConnected}}
}

func (fakeStatus) Events() *tunnel.EventBuffer {
	b := tunnel.NewEventBuffer(16)
	b.Append(model.AccessEvent{Hostname: "app.relay.test", Decision: model.DecisionDeny, DenyReason: model.DenyIPPolicy})
	return b
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(st, fakeStatus{}, "test", nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	req, _ := http.NewRequest(method, srv.URL+path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(data)
}

func TestRelayAndRouteLifecycle(t *testing.T) {
	srv := newServer(t)

	// Create a relay.
	resp, body := do(t, srv, "POST", "/api/relays",
		`{"name":"vps","host":"relay.example.com","port":7443,"token":"lt_a_b"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create relay: %d %s", resp.StatusCode, body)
	}
	var relay model.Relay
	if err := json.Unmarshal([]byte(body), &relay); err != nil {
		t.Fatal(err)
	}

	// Create a tunnel route bound to it.
	routeBody := `{"name":"app","target":"127.0.0.1:3000","exposure":"tunnel","kind":"http","relayId":"` +
		relay.ID + `","hostnames":["app.relay.example.com"]}`
	resp, body = do(t, srv, "POST", "/api/routes", routeBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create route: %d %s", resp.StatusCode, body)
	}
	var route model.Route
	_ = json.Unmarshal([]byte(body), &route)
	if route.Target != "127.0.0.1:3000" || route.Exposure != model.ExposureTunnel {
		t.Errorf("route = %+v", route)
	}

	// List routes.
	resp, body = do(t, srv, "GET", "/api/routes", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "app.relay.example.com") {
		t.Errorf("list routes: %d %s", resp.StatusCode, body)
	}

	// Status includes the fake relay.
	resp, body = do(t, srv, "GET", "/api/status", "")
	if resp.StatusCode != 200 || !strings.Contains(body, "rly_x") {
		t.Errorf("status: %d %s", resp.StatusCode, body)
	}

	// Delete the route.
	resp, _ = do(t, srv, "DELETE", "/api/routes/"+route.ID, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete route: %d", resp.StatusCode)
	}
}

func TestValidationErrorIsStructured(t *testing.T) {
	srv := newServer(t)
	// Tunnel route with a non-existent relay → validation failure.
	resp, body := do(t, srv, "POST", "/api/routes",
		`{"name":"x","target":"127.0.0.1:3000","exposure":"tunnel","kind":"http","relayId":"rly_missing","hostnames":["a.b.com"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "validation failed") {
		t.Errorf("body = %s", body)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	srv := newServer(t)
	resp, _ := do(t, srv, "POST", "/api/relays", `{"name":"x","host":"h","token":"t","bogus":1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
}

func TestRequireLoopback(t *testing.T) {
	if err := requireLoopback("127.0.0.1:4040"); err != nil {
		t.Errorf("loopback rejected: %v", err)
	}
	if err := requireLoopback("0.0.0.0:4040"); err == nil {
		t.Error("non-loopback accepted")
	}
}

func TestListenAndServeRefusesNonLoopback(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	t.Cleanup(func() { _ = st.Close() })
	s := New(st, fakeStatus{}, "test", nil)
	if err := s.ListenAndServe(context.Background(), "0.0.0.0:0"); err == nil {
		t.Error("expected refusal to bind non-loopback")
	}
}
