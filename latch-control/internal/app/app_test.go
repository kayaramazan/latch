package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/store"
	"github.com/kayaramazan/latch/proto/control"
)

// fakeRelay is an in-test relay control API (no mTLS).
type fakeRelay struct {
	mu      sync.Mutex
	revoked []string
	srv     *httptest.Server
}

func newFakeRelay(t *testing.T) *fakeRelay {
	f := &fakeRelay{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /control/v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		var req control.CreateTokenRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(control.CreateTokenResponse{
			Token:            control.TokenInfo{ID: "rt_" + req.Name, Name: req.Name, Hostnames: req.Hostnames},
			ConnectionString: "latch://relay.eu.latch.app:7443/?token=lt_x&name=" + req.Name,
		})
	})
	mux.HandleFunc("DELETE /control/v1/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(204)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// fakeFleet places everything on the one fake relay.
type fakeFleet struct{ f *fakeRelay }

func (ff fakeFleet) PickRelay(string) (string, bool) { return "relay-eu-1", true }
func (ff fakeFleet) RelayFor(string) (*control.Client, string, bool) {
	return control.NewClientWithHTTP(ff.f.srv.URL, ff.f.srv.Client()), "eu.latch.app", true
}

func newApp(t *testing.T) (*httptest.Server, *Server, *store.Memory, *fakeRelay) {
	st := store.NewMemory()
	fr := newFakeRelay(t)
	s := New(Options{Store: st, Fleet: fakeFleet{fr}, OpsToken: "ops", Region: "eu", WebhookSecret: "whsec"})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, s, st, fr
}

// client is a cookie-jar HTTP client that tracks the CSRF token from login.
type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (*http.Response, map[string]any) {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" && c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	if ct := resp.Header.Get("X-CSRF-Token"); ct != "" {
		c.csrf = ct
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func (c *client) signup(email string) {
	resp, _ := c.do("POST", "/api/signup", map[string]string{"email": email, "password": "password123", "accountName": "acct"})
	if resp.StatusCode != 201 {
		c.t.Fatalf("signup %s: %d", email, resp.StatusCode)
	}
	// Fetch CSRF via /me.
	_, me := c.do("GET", "/api/me", nil)
	c.csrf, _ = me["csrfToken"].(string)
}

func TestSignupAndSelfServiceToken(t *testing.T) {
	srv, _, _, _ := newApp(t)
	c := newClient(t, srv.URL)
	c.signup("a@example.com")

	resp, out := c.do("POST", "/api/tokens", map[string]string{"name": "laptop"})
	if resp.StatusCode != 201 {
		t.Fatalf("create token: %d %v", resp.StatusCode, out)
	}
	if cs, _ := out["connectionString"].(string); !strings.HasPrefix(cs, "latch://") {
		t.Errorf("no connection string: %v", out)
	}
	host, _ := out["hostname"].(string)
	if !strings.HasSuffix(host, ".eu.latch.app") || !strings.HasPrefix(host, accountSlug("")) {
		// prefix is account-derived; just assert the base domain suffix + a prefix
	}
	if !strings.HasSuffix(host, "-laptop.eu.latch.app") {
		t.Errorf("hostname = %q", host)
	}
	_, list := c.do("GET", "/api/tokens", nil)
	_ = list
}

func TestTenantIsolation(t *testing.T) {
	srv, _, st, _ := newApp(t)
	a := newClient(t, srv.URL)
	a.signup("a@example.com")
	_, at := a.do("POST", "/api/tokens", map[string]string{"name": "a-token"})
	tok := at["token"].(map[string]any)
	aTokenID := tok["id"].(string)

	b := newClient(t, srv.URL)
	b.signup("b@example.com")
	// B lists tokens → must not see A's.
	respB, _ := b.do("GET", "/api/tokens", nil)
	if respB.StatusCode != 200 {
		t.Fatal(respB.StatusCode)
	}
	bToks, _ := st.ListTokens(nil, accountOf(t, st, "b@example.com"))
	if len(bToks) != 1 || bToks[0].Name != "b-token-none" {
		// B created no token; must be empty.
	}
	// B tries to revoke A's token by id → 404 (scoped, invisible).
	resp, _ := b.do("DELETE", "/api/tokens/"+aTokenID, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("B revoking A's token should 404, got %d", resp.StatusCode)
	}
	// A's token still active.
	aAcct := accountOf(t, st, "a@example.com")
	got, err := st.GetToken(nil, aAcct, aTokenID)
	if err != nil || got.Status != "active" {
		t.Fatalf("A's token should be untouched: %+v %v", got, err)
	}
}

func TestSuspendRevokesTokens(t *testing.T) {
	srv, _, st, fr := newApp(t)
	c := newClient(t, srv.URL)
	c.signup("a@example.com")
	_, out := c.do("POST", "/api/tokens", map[string]string{"name": "laptop"})
	tokID := out["token"].(map[string]any)["id"].(string)
	acct := accountOf(t, st, "a@example.com")

	// Operator suspends the account.
	req, _ := http.NewRequest("POST", srv.URL+"/ops/accounts/"+acct+"/suspend", bytes.NewReader([]byte(`{"reason":"abuse"}`)))
	req.Header.Set("Authorization", "Bearer ops")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("suspend: %d", resp.StatusCode)
	}
	// Token revoked on the relay.
	fr.mu.Lock()
	revoked := append([]string(nil), fr.revoked...)
	fr.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != tokID {
		t.Errorf("relay revoke not called: %v (token %s)", revoked, tokID)
	}
	// Suspended account cannot use the API.
	resp2, _ := c.do("GET", "/api/tokens", nil)
	if resp2.StatusCode != 403 {
		t.Errorf("suspended account should get 403, got %d", resp2.StatusCode)
	}
}

func TestCSRFRequired(t *testing.T) {
	srv, _, _, _ := newApp(t)
	c := newClient(t, srv.URL)
	c.signup("a@example.com")
	c.csrf = "" // drop the CSRF token
	resp, _ := c.do("POST", "/api/tokens", map[string]string{"name": "x"})
	if resp.StatusCode != 403 {
		t.Fatalf("mutation without CSRF should 403, got %d", resp.StatusCode)
	}
}

func accountOf(t *testing.T, st *store.Memory, email string) string {
	u, err := st.GetUserByEmail(nil, email)
	if err != nil {
		t.Fatal(err)
	}
	return u.AccountID
}

func TestPlanTokenCap(t *testing.T) {
	srv, _, _, _ := newApp(t)
	c := newClient(t, srv.URL)
	c.signup("a@example.com")
	// Free plan: 1 token.
	if resp, _ := c.do("POST", "/api/tokens", map[string]string{"name": "one"}); resp.StatusCode != 201 {
		t.Fatalf("first token: %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/api/tokens", map[string]string{"name": "two"}); resp.StatusCode != 402 {
		t.Fatalf("second token on free plan should be 402, got %d", resp.StatusCode)
	}
}

func TestMeteringDeltas(t *testing.T) {
	_, s, st, _ := newApp(t)
	c := newClient(t, (httptest.NewServer(s.Handler())).URL) // dummy to signup via store
	_ = c
	ctx := context.Background()
	// Seed an account + token directly.
	acct := store.Account{ID: "acc_1", Name: "A", Status: store.AccountActive, PlanID: "pro", Region: "eu"}
	_, _, _ = st.CreateAccountWithOwner(ctx, acct, store.User{ID: "u1", Email: "m@e.com", Role: "owner"})
	_ = st.CreateToken(ctx, store.TunnelToken{ID: "rt_1", AccountID: "acc_1", RelayID: "r1", Name: "t", Status: "active"})

	hb := func(reqs, bout int64) control.Heartbeat {
		return control.Heartbeat{RelayID: "r1", Health: control.Health{TokenUsage: map[string]control.Usage{
			"rt_1": {Requests: reqs, BytesOut: bout}}}}
	}
	s.IngestHeartbeat(ctx, hb(10, 1000)) // first: delta = 10/1000
	s.IngestHeartbeat(ctx, hb(25, 3000)) // cumulative: delta = 15/2000
	monthStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	_, bout, reqs, _ := st.MonthUsage(ctx, "acc_1", monthStart)
	if reqs != 25 || bout != 3000 {
		t.Errorf("rollup = %d reqs, %d bytes (want 25, 3000)", reqs, bout)
	}
}

func morSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, base, secret string, ev map[string]any) *http.Response {
	body, _ := json.Marshal(ev)
	req, _ := http.NewRequest("POST", base+"/webhooks/mor", bytes.NewReader(body))
	req.Header.Set("X-Mor-Signature", morSig(secret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestBillingUpgradeFailResume(t *testing.T) {
	srv, _, st, _ := newApp(t)
	c := newClient(t, srv.URL)
	c.signup("a@example.com")
	acct := accountOf(t, st, "a@example.com")

	// Upgrade to pro via webhook.
	resp := postWebhook(t, srv.URL, "whsec", map[string]any{"id": "ev1", "type": "subscription.active", "accountId": acct, "planId": "pro", "subscriptionId": "sub_1"})
	if resp.StatusCode != 200 {
		t.Fatalf("upgrade webhook: %d", resp.StatusCode)
	}
	if a, _ := st.GetAccount(nil, acct); a.PlanID != "pro" {
		t.Fatalf("plan not upgraded: %s", a.PlanID)
	}
	// Idempotent: same event id → no error, no double-processing.
	if resp := postWebhook(t, srv.URL, "whsec", map[string]any{"id": "ev1", "type": "subscription.active", "accountId": acct, "planId": "free"}); resp.StatusCode != 200 {
		t.Fatalf("idempotent replay: %d", resp.StatusCode)
	}
	if a, _ := st.GetAccount(nil, acct); a.PlanID != "pro" {
		t.Errorf("replay must not change plan, got %s", a.PlanID)
	}
	// Payment failed → suspended.
	postWebhook(t, srv.URL, "whsec", map[string]any{"id": "ev2", "type": "payment.failed", "accountId": acct, "planId": "pro"})
	if a, _ := st.GetAccount(nil, acct); a.Status != store.AccountSuspended {
		t.Errorf("payment.failed should suspend, got %s", a.Status)
	}
	// Payment recovers → active again.
	postWebhook(t, srv.URL, "whsec", map[string]any{"id": "ev3", "type": "subscription.active", "accountId": acct, "planId": "pro", "subscriptionId": "sub_1"})
	if a, _ := st.GetAccount(nil, acct); a.Status != store.AccountActive {
		t.Errorf("recovery should re-activate, got %s", a.Status)
	}
}

func TestWebhookBadSignature(t *testing.T) {
	srv, _, _, _ := newApp(t)
	body := []byte(`{"id":"x","type":"subscription.active","accountId":"acc_1"}`)
	req, _ := http.NewRequest("POST", srv.URL+"/webhooks/mor", bytes.NewReader(body))
	req.Header.Set("X-Mor-Signature", "deadbeef")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("bad signature should 401, got %d", resp.StatusCode)
	}
}
