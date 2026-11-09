package tunnel

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latchd/internal/store"
	"github.com/kayaramazan/latch/proto/model"
)

const testHost = "app.relay.test"

// echoServer returns the request line, body, and selected headers; it also
// upgrades WebSocket requests and echoes frames.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = buf.Flush()
			_, _ = io.Copy(conn, buf.Reader)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-XFF", r.Header.Get("X-Forwarded-For"))
		w.WriteHeader(200)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setup wires a store + manager + test relay and returns them. The route with
// id "rt_app" is announced for testHost → target.
func setup(t *testing.T, target string) (*testRelay, *store.Store) {
	t.Helper()
	tr := newTestRelay(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	relay := tr.relayModel("test-relay", "lt_abc_secret")
	route := model.NewRoute("app", target, model.ExposureTunnel)
	route.RelayID = relay.ID
	route.Hostnames = []string{testHost}
	if _, err := st.Update(context.Background(), "test", "seed", func(snap *model.Snapshot) error {
		snap.Relays = append(snap.Relays, relay)
		snap.Routes = append(snap.Routes, route)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(Deps{Store: st, AgentID: "ag_testtesttesttesttesttest", AgentVersion: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mgr.Run(ctx)
	return tr, st
}

// get issues a public request for host via the relay's trusting TLS client.
func get(t *testing.T, tr *testRelay, host, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", tr.publicURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := tr.public.Client().Do(req)
	if err != nil {
		return nil, ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", d)
}

func TestEndToEndHTTP(t *testing.T) {
	echo := echoServer(t)
	tr, _ := setup(t, echo.Listener.Addr().String())

	// The agent connects and announces; the route becomes reachable.
	waitFor(t, 5*time.Second, func() bool {
		resp, body := get(t, tr, testHost, "/hello?x=1")
		return resp != nil && resp.StatusCode == 200 && body == "GET /hello?x=1 "
	})
	resp, _ := get(t, tr, testHost, "/hello")
	if resp.Header.Get("X-Seen-Host") != testHost {
		t.Errorf("Host forwarded = %q", resp.Header.Get("X-Seen-Host"))
	}
	if xff := resp.Header.Get("X-Seen-XFF"); xff == "" || !strings.HasPrefix(xff, "127.0.0.1") {
		t.Errorf("X-Forwarded-For = %q", xff)
	}
}

func TestLocalTargetDownReturns502(t *testing.T) {
	// Pick a closed port by opening then closing a listener.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	_ = ln.Close()
	tr, _ := setup(t, closed)

	waitFor(t, 5*time.Second, func() bool {
		resp, _ := get(t, tr, testHost, "/")
		return resp != nil && resp.StatusCode == http.StatusBadGateway
	})
}

func TestReconnectAfterRelayOutage(t *testing.T) {
	echo := echoServer(t)
	tr, _ := setup(t, echo.Listener.Addr().String())

	waitFor(t, 5*time.Second, func() bool {
		resp, _ := get(t, tr, testHost, "/")
		return resp != nil && resp.StatusCode == 200
	})

	// Relay goes down: existing session drops, new connections refused.
	tr.setDown(true)
	waitFor(t, 3*time.Second, func() bool {
		resp, _ := get(t, tr, testHost, "/")
		return resp == nil || resp.StatusCode != 200
	})

	// Relay comes back; the agent must reconnect and re-announce on its own.
	start := time.Now()
	tr.setDown(false)
	waitFor(t, 10*time.Second, func() bool {
		resp, body := get(t, tr, testHost, "/")
		return resp != nil && resp.StatusCode == 200 && body == "GET / "
	})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("reconnect took %s", elapsed)
	}
}

func TestWebSocketPassthrough(t *testing.T) {
	echo := echoServer(t)
	tr, _ := setup(t, echo.Listener.Addr().String())
	waitFor(t, 5*time.Second, func() bool {
		resp, _ := get(t, tr, testHost, "/")
		return resp != nil && resp.StatusCode == 200
	})

	u := strings.TrimPrefix(tr.publicURL, "https://")
	conn, err := tlsDial(t, tr, u)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /chat HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", testHost)
	br := bufio.NewReader(conn)
	wsResp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if wsResp.StatusCode != 101 {
		t.Fatalf("ws status %d", wsResp.StatusCode)
	}
	for _, msg := range []string{"hello", "world"} {
		fmt.Fprint(conn, msg)
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(br, buf); err != nil || string(buf) != msg {
			t.Fatalf("echo %q: got %q, %v", msg, buf, err)
		}
	}
}

func TestVersionMismatchReported(t *testing.T) {
	tr := newTestRelay(t)
	tr.forceVerErr.Store(true)
	st, err := store.Open(filepath.Join(t.TempDir(), "latchd.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	relay := tr.relayModel("test-relay", "lt_abc_secret")
	cl := NewClient(relay, "ag_testtesttesttesttesttest", "test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cl.Run(ctx)
	waitFor(t, 5*time.Second, func() bool {
		return strings.Contains(cl.Status().LastError, "protocol mismatch")
	})
}

func tlsDial(t *testing.T, tr *testRelay, addr string) (net.Conn, error) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(tr.public.Certificate())
	host, _, _ := net.SplitHostPort(addr)
	return tls.Dial("tcp", addr, &tls.Config{RootCAs: pool, ServerName: host})
}
