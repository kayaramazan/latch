package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// testRelay is an in-process, protocol-conformant relay for end-to-end tests:
// a TLS agent listener plus a public HTTPS server that proxies requests over
// the tunnel. It is intentionally minimal (no policies, no persistence).
type testRelay struct {
	t         *testing.T
	agentLn   net.Listener
	tlsCert   tls.Certificate
	caPEM     string
	public    *httptest.Server
	publicURL string
	proxy     *httputil.ReverseProxy

	down        atomic.Bool
	forceVerErr atomic.Bool

	mu        sync.Mutex
	byHost    map[string]*relaySess
	hostRoute map[string]string
	sessions  map[*relaySess]struct{}
	seq       int
}

type relaySess struct {
	r    *testRelay
	mux  *yamux.Session
	ctrl *tunnel.Codec
	conn net.Conn
}

func newTestRelay(t *testing.T) *testRelay {
	t.Helper()
	cert, caPEM := genCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert}, NextProtos: []string{tunnel.ALPN},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &testRelay{
		t: t, agentLn: ln, tlsCert: cert, caPEM: caPEM,
		byHost: map[string]*relaySess{}, hostRoute: map[string]string{}, sessions: map[*relaySess]struct{}{},
	}
	r.public = httptest.NewUnstartedServer(http.HandlerFunc(r.servePublic))
	r.proxy = &httputil.ReverseProxy{
		Transport:     &http.Transport{DialContext: r.dialTunnel, DisableKeepAlives: true},
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "tunnel"})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	r.public.StartTLS()
	r.publicURL = r.public.URL
	t.Cleanup(r.public.Close)
	go r.acceptAgents()
	t.Cleanup(func() { _ = r.agentLn.Close() })
	return r
}

func (r *testRelay) host() string {
	h, _, _ := net.SplitHostPort(r.agentLn.Addr().String())
	return h
}

func (r *testRelay) port() int {
	_, p, _ := net.SplitHostPort(r.agentLn.Addr().String())
	n, _ := net.LookupPort("tcp", p)
	return n
}

// relayModel builds the store relay record a client connects with.
func (r *testRelay) relayModel(name, token string) model.Relay {
	return model.Relay{ID: model.NewID(model.PrefixRelay), Name: name, Host: r.host(), Port: r.port(), Token: token, CAPEM: r.caPEM}
}

func (r *testRelay) setDown(down bool) {
	r.down.Store(down)
	if down {
		r.closeSessions()
	}
}

func (r *testRelay) closeSessions() {
	r.mu.Lock()
	var list []*relaySess
	for s := range r.sessions {
		list = append(list, s)
	}
	r.mu.Unlock()
	for _, s := range list {
		_ = s.conn.Close()
	}
}

func (r *testRelay) acceptAgents() {
	for {
		conn, err := r.agentLn.Accept()
		if err != nil {
			return
		}
		if r.down.Load() {
			_ = conn.Close()
			continue
		}
		go r.serveAgent(conn)
	}
}

func (r *testRelay) serveAgent(conn net.Conn) {
	tc := conn.(*tls.Conn)
	if err := tc.Handshake(); err != nil {
		_ = conn.Close()
		return
	}
	mux, err := yamux.Server(conn, tunnel.YamuxConfig())
	if err != nil {
		_ = conn.Close()
		return
	}
	ctrlStream, err := mux.AcceptStream()
	if err != nil {
		_ = mux.Close()
		return
	}
	s := &relaySess{r: r, mux: mux, ctrl: tunnel.NewCodec(ctrlStream), conn: conn}

	env, err := s.ctrl.Recv()
	if err != nil || env.T != tunnel.TypeHello {
		_ = mux.Close()
		return
	}
	var hello tunnel.Hello
	_ = env.Decode(&hello)
	if hello.Token == "" || hello.AgentID == "" {
		_ = s.ctrl.SendError(env.ID, tunnel.ErrAuth, "token required", true)
		_ = mux.Close()
		return
	}
	if r.forceVerErr.Load() {
		_, _ = s.ctrl.Send(tunnel.TypeError, env.ID, &tunnel.Error{Code: tunnel.ErrVersion, Fatal: true,
			Message: "unsupported", RelayMin: 2, RelayMax: 2})
		_ = mux.Close()
		return
	}
	_, _ = s.ctrl.Send(tunnel.TypeHelloAck, env.ID, &tunnel.HelloAck{
		Proto: 1, RelayVersion: "test", ServerTime: time.Now().UTC(),
		Scope:      tunnel.Scope{Hostnames: []string{"*"}, TCP: true, TCPPortRange: []int{20000, 20999}},
		BaseDomain: "relay.test", Capabilities: []string{tunnel.CapTCP, tunnel.CapEvents},
	})

	r.mu.Lock()
	r.sessions[s] = struct{}{}
	r.mu.Unlock()

	s.readLoop()

	r.mu.Lock()
	delete(r.sessions, s)
	for h, cur := range r.byHost {
		if cur == s {
			delete(r.byHost, h)
		}
	}
	r.mu.Unlock()
	_ = mux.Close()
}

func (s *relaySess) readLoop() {
	for {
		env, err := s.ctrl.Recv()
		if err != nil {
			return
		}
		switch env.T {
		case tunnel.TypePing:
			var p tunnel.Ping
			_ = env.Decode(&p)
			_, _ = s.ctrl.Send(tunnel.TypePong, env.ID, &tunnel.Pong{TS: p.TS})
		case tunnel.TypePong:
		case tunnel.TypeRouteAnnounce:
			var ra tunnel.RouteAnnounce
			_ = env.Decode(&ra)
			ack := s.r.announce(s, ra.Routes)
			_, _ = s.ctrl.Send(tunnel.TypeRouteAck, env.ID, &ack)
		case tunnel.TypeGoodbye:
			return
		}
	}
}

// announce registers the session's hostnames (full-set semantics).
func (r *testRelay) announce(s *relaySess, specs []tunnel.RouteSpec) tunnel.RouteAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Drop this session's old hostnames.
	for h, cur := range r.byHost {
		if cur == s {
			delete(r.byHost, h)
			delete(r.hostRoute, h)
		}
	}
	ack := tunnel.RouteAck{Accepted: []tunnel.RouteAccepted{}, Rejected: []tunnel.RouteRejected{}}
	for _, spec := range specs {
		acc := tunnel.RouteAccepted{RouteID: spec.RouteID, Hostnames: spec.Hostnames}
		for _, h := range spec.Hostnames {
			r.byHost[h] = s
			r.hostRoute[h] = spec.RouteID
			acc.PublicURLs = append(acc.PublicURLs, r.publicURL+"/")
		}
		ack.Accepted = append(ack.Accepted, acc)
	}
	return ack
}

func (r *testRelay) sessionFor(host string) *relaySess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byHost[host]
}

// --- public side ---

type relayDialKey struct{}

func (r *testRelay) servePublic(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	s := r.sessionFor(host)
	if s == nil {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	hdr := &tunnel.StreamHeader{RouteID: r.routeFor(host), Kind: model.KindHTTP, Hostname: host,
		ClientIP: clientIP(req.RemoteAddr), TLS: true, StreamID: "st_test"}
	ctx := context.WithValue(req.Context(), relayDialKey{}, &relayDial{s: s, hdr: hdr})
	r.proxy.ServeHTTP(w, req.WithContext(ctx))
}

func (r *testRelay) routeFor(host string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hostRoute[host]
}

type relayDial struct {
	s   *relaySess
	hdr *tunnel.StreamHeader
}

func (r *testRelay) dialTunnel(ctx context.Context, _, _ string) (net.Conn, error) {
	d, _ := ctx.Value(relayDialKey{}).(*relayDial)
	if d == nil {
		return nil, errors.New("no tunnel target")
	}
	st, err := d.s.mux.OpenStream()
	if err != nil {
		return nil, err
	}
	_ = st.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tunnel.WriteStreamHeader(st, d.hdr); err != nil {
		_ = st.Close()
		return nil, err
	}
	status, err := tunnel.ReadStatus(st)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	_ = st.SetDeadline(time.Time{})
	if status != tunnel.StatusOK {
		_ = st.Close()
		return nil, errors.New("agent status " + status.String())
	}
	return st, nil
}

func clientIP(remote string) string {
	h, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return h
}

// --- cert ---

func genCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "latch-relay-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"relay.test", "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, string(certPEM)
}
