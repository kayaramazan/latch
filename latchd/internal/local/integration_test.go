package local

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return p
}

// readInternalCA waits for Caddy's local CA root to appear and returns a pool.
func readInternalCA(t *testing.T, storageDir string) *x509.CertPool {
	t.Helper()
	path := filepath.Join(storageDir, "pki", "authorities", "local", "root.crt")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pem, err := os.ReadFile(path); err == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				return pool
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("internal CA root never appeared at %s", path)
	return nil
}

func TestLocalModeEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "UPSTREAM %s", r.URL.Path)
	}))
	defer upstream.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rt := model.NewRoute("site", upstream.Listener.Addr().String(), model.ExposureLocal)
	rt.Hostnames = []string{"site.test"}
	rt.Headers = []model.HeaderRule{{Phase: model.HeaderResponse, Op: model.HeaderSet, Name: "X-Served", Value: "latch"}}
	if _, err := st.Update(context.Background(), "test", "seed", func(snap *model.Snapshot) error {
		snap.Routes = append(snap.Routes, rt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	httpsPort := freePort(t)
	storageDir := t.TempDir()
	var mu sync.Mutex
	var events []model.AccessEvent
	emit := func(ev model.AccessEvent) { mu.Lock(); events = append(events, ev); mu.Unlock() }

	m := NewManager(Deps{
		Store: st, Emit: emit,
		Options: Options{HTTPPort: freePort(t), HTTPSPort: httpsPort, Issuer: IssuerInternal, StorageDir: storageDir},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	t.Cleanup(func() { cancel(); time.Sleep(200 * time.Millisecond) })

	pool := readInternalCA(t, storageDir)
	addr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	get := func() (int, string, http.Header) {
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "site.test", RootCAs: pool})
		if err != nil {
			return 0, err.Error(), nil
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(conn, "GET /hello HTTP/1.1\r\nHost: site.test\r\nConnection: close\r\n\r\n")
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			return 0, err.Error(), nil
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body), resp.Header
	}

	// Allow path: HTTPS reaches the upstream, response header applied.
	var status int
	var body string
	var hdr http.Header
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, body, hdr = get()
		if status == 200 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if status != 200 || body != "UPSTREAM /hello" {
		t.Fatalf("expected 200 UPSTREAM /hello, got %d %q", status, body)
	}
	if hdr.Get("X-Served") != "latch" {
		t.Errorf("response header rule not applied: %v", hdr)
	}

	// Add an IP policy denying loopback, bound to the route: same engine, 403.
	pol := model.IPPolicy{ID: model.NewID(model.PrefixIPPolicy), Name: "block",
		Rules: []model.IPRule{{Action: model.ActionDeny, CIDR: "127.0.0.0/8"}}}
	if _, err := st.Update(context.Background(), "test", "deny", func(snap *model.Snapshot) error {
		snap.IPPolicies = append(snap.IPPolicies, pol)
		for i := range snap.Routes {
			if snap.Routes[i].ID == rt.ID {
				snap.Routes[i].IPPolicyID = pol.ID
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _ = get()
		if status == http.StatusForbidden {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 after IP deny, got %d", status)
	}

	mu.Lock()
	var sawDeny, sawAllow bool
	for _, ev := range events {
		if ev.Where != "agent" || ev.Source != model.SourceLocal {
			t.Errorf("event should be local/agent: %+v", ev)
		}
		if ev.Decision == model.DecisionDeny && ev.DenyReason == model.DenyIPPolicy {
			sawDeny = true
		}
		if ev.Decision == model.DecisionAllow && ev.Status == 200 {
			sawAllow = true
		}
	}
	mu.Unlock()
	if !sawAllow || !sawDeny {
		t.Errorf("expected both allow and deny local events (allow=%v deny=%v)", sawAllow, sawDeny)
	}
}
