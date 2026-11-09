package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kayaramazan/latch/latchd/internal/events"
	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(h, "s3cret") {
		t.Error("correct password rejected")
	}
	if verifyPassword(h, "wrong") {
		t.Error("wrong password accepted")
	}
}

// serverWithAuth returns a server whose config requires a password.
func serverWithAuth(t *testing.T, password string) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hash, _ := HashPassword(password)
	if _, err := st.Update(context.Background(), "test", "set pw", func(snap *model.Snapshot) error {
		snap.Settings.Auth = model.AuthSettings{Mode: model.AuthPassword, PasswordHash: hash}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ev, _ := events.Open(filepath.Join(t.TempDir(), "events.db"), events.Options{}, nil)
	t.Cleanup(func() { _ = ev.Close() })
	s := New(st, fakeStatus{}, ev, "test", nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthRequiredAndLogin(t *testing.T) {
	srv := serverWithAuth(t, "hunter2")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Unauthenticated read is rejected.
	resp, _ := client.Get(srv.URL + "/api/routes")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong password.
	resp, _ = client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader([]byte(`{"password":"nope"}`)))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct password → session cookie + csrf token.
	resp, _ = client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader([]byte(`{"password":"hunter2"}`)))
	if resp.StatusCode != 200 {
		t.Fatalf("login: got %d", resp.StatusCode)
	}
	var lr struct {
		CSRFToken string `json:"csrfToken"`
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_ = json.Unmarshal(body, &lr)
	if lr.CSRFToken == "" {
		t.Fatal("no csrf token returned")
	}

	// Authenticated read works now.
	resp, _ = client.Get(srv.URL + "/api/routes")
	if resp.StatusCode != 200 {
		t.Fatalf("authed read: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Mutation without CSRF is rejected.
	req, _ := http.NewRequest("POST", srv.URL+"/api/ip-policies", bytes.NewReader([]byte(`{"name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("mutation without CSRF: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Mutation with CSRF succeeds.
	req, _ = http.NewRequest("POST", srv.URL+"/api/ip-policies", bytes.NewReader([]byte(`{"name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, lr.CSRFToken)
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mutation with CSRF: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// /api/system is always open.
	resp, _ = http.Get(srv.URL + "/api/system")
	if resp.StatusCode != 200 {
		t.Errorf("system should be open: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestOriginCSRFDefense(t *testing.T) {
	srv := newServer(t) // no auth
	req, _ := http.NewRequest("POST", srv.URL+"/api/ip-policies", bytes.NewReader([]byte(`{"name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, _ := srv.Client().Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin mutation should be refused, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestNonLoopbackRequiresPassword(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	t.Cleanup(func() { _ = st.Close() })
	ev, _ := events.Open(filepath.Join(t.TempDir(), "events.db"), events.Options{}, nil)
	t.Cleanup(func() { _ = ev.Close() })
	s := New(st, fakeStatus{}, ev, "test", nil)
	if err := s.requireAuthOrLoopback("0.0.0.0:4040"); err == nil {
		t.Error("non-loopback without password should be refused")
	}
	// With a password set it is allowed.
	hash, _ := HashPassword("pw")
	_, _ = st.Update(context.Background(), "t", "pw", func(snap *model.Snapshot) error {
		snap.Settings.Auth = model.AuthSettings{Mode: model.AuthPassword, PasswordHash: hash}
		return nil
	})
	if err := s.requireAuthOrLoopback("0.0.0.0:4040"); err != nil {
		t.Errorf("non-loopback with password should be allowed: %v", err)
	}
}
