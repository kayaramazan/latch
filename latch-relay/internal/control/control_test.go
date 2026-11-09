package control

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	"github.com/kayaramazan/latch/latch-relay/internal/registry"
	"github.com/kayaramazan/latch/latch-relay/internal/session"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/latch-relay/internal/usage"
	pc "github.com/kayaramazan/latch/proto/control"
)

// --- test CA / certs ---

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) *ca {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &ca{cert: cert, key: key, pool: pool}
}

func (c *ca) issue(t *testing.T, cn string, server bool) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{cn}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func newServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reg := registry.New(st, registry.Options{PublicHost: "relay.test", HTTPSPort: 443, TCPPortRange: [2]int{20000, 20999}})
	a := auth.New(st)
	sess := session.NewManager(session.Deps{Auth: a, Registry: reg, RelayVersion: "test", BaseDomain: "relay.test"})
	return New(Deps{
		Auth: a, Store: st, Sessions: sess, Registry: reg, Usage: usage.New(),
		RelayID: "relay-1", Region: "eu", Version: "test", PublicHost: "relay.test",
		BaseDomain: "relay.test", AgentPort: 7443,
	})
}

func TestMTLSAndTokenLifecycle(t *testing.T) {
	ca := newCA(t)
	serverCert := ca.issue(t, "relay.test", true)
	clientCert := ca.issue(t, "control-plane", false)

	srv := httptest.NewUnstartedServer(newServer(t).Handler())
	srv.TLS = MTLSConfig(serverCert, ca.pool)
	srv.StartTLS()
	defer srv.Close()

	// A client WITHOUT a cert is rejected at the TLS layer.
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.pool}}}
	if _, err := noCert.Get(srv.URL + pc.APIVersion + "/health"); err == nil {
		t.Fatal("expected TLS failure without a client certificate")
	}

	// The control plane (valid client cert) can drive the relay.
	cp := pc.NewClientWithHTTP(srv.URL, &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca.pool, Certificates: []tls.Certificate{clientCert}}}})
	ctx := context.Background()

	res, err := cp.CreateToken(ctx, pc.CreateTokenRequest{Name: "acct-1", Hostnames: []string{"*.eu.latch.app"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ConnectionString == "" || res.Token.ID == "" {
		t.Fatalf("bad create response: %+v", res)
	}

	toks, err := cp.ListTokens(ctx)
	if err != nil || len(toks) != 1 || toks[0].Name != "acct-1" {
		t.Fatalf("list tokens: %v %+v", err, toks)
	}

	if err := cp.RevokeToken(ctx, res.Token.ID); err != nil {
		t.Fatal(err)
	}

	h, err := cp.Health(ctx)
	if err != nil || h.RelayID != "relay-1" || h.Region != "eu" {
		t.Fatalf("health: %v %+v", err, h)
	}
}

func TestDomainCRUD(t *testing.T) {
	ca := newCA(t)
	srv := httptest.NewUnstartedServer(newServer(t).Handler())
	srv.TLS = MTLSConfig(ca.issue(t, "relay.test", true), ca.pool)
	srv.StartTLS()
	defer srv.Close()
	cp := pc.NewClientWithHTTP(srv.URL, &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: ca.pool, Certificates: []tls.Certificate{ca.issue(t, "cp", false)}}}})
	ctx := context.Background()
	if _, err := cp.CreateDomain(ctx, pc.CreateDomainRequest{Domain: "app.customer.com"}); err != nil {
		t.Fatal(err)
	}
	ds, err := cp.ListDomains(ctx)
	if err != nil || len(ds) != 1 || ds[0].Domain != "app.customer.com" || ds[0].Target != "relay.test" {
		t.Fatalf("domains: %v %+v", err, ds)
	}
}
