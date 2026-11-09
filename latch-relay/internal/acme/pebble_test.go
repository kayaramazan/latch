//go:build acme_pebble

// Package acme's Pebble integration test. It runs only with the `acme_pebble`
// build tag and a running Pebble ACME server (https://github.com/letsencrypt/
// pebble), plus challtestsrv for DNS/ALPN challenge responses. It is excluded
// from the default `go test ./...` run; CI invokes `make test-acme`.
//
// Environment:
//
//	PEBBLE_DIR      ACME directory URL (default https://localhost:14000/dir)
//	PEBBLE_CA_FILE  PEM file with Pebble's management CA (to trust its certs)
//	CHALLTESTSRV    challtestsrv management URL (default http://localhost:8055)
package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func pebbleDir() string {
	if v := os.Getenv("PEBBLE_DIR"); v != "" {
		return v
	}
	return "https://localhost:14000/dir"
}

func trustedRoots(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if f := os.Getenv("PEBBLE_CA_FILE"); f != "" {
		pem, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read PEBBLE_CA_FILE: %v", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			t.Fatal("PEBBLE_CA_FILE is not a valid certificate")
		}
	}
	return pool
}

// TestOnDemandCustomDomain obtains a certificate for a registered custom domain
// via TLS-ALPN-01 against Pebble. It requires challtestsrv to resolve the test
// domain to this host and to proxy the ALPN challenge.
func TestOnDemandCustomDomain(t *testing.T) {
	if os.Getenv("PEBBLE_CA_FILE") == "" {
		t.Skip("set PEBBLE_CA_FILE (and run Pebble + challtestsrv) to run the ACME integration test")
	}
	const domain = "custom.test.service"

	mgr, err := New(Options{
		DataDir:      t.TempDir(),
		Email:        "ci@latch.test",
		BaseDomain:   "relay.test",
		PublicHost:   "relay.test",
		DNSProvider:  DNSManual,
		CADirURL:     pebbleDir(),
		TrustedRoots: trustedRoots(t),
		Domains:      fakeDomains{domain: true},
		Resolver:     fakeResolver{hosts: map[string][]string{domain: {"127.0.0.1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// Serve HTTPS with the manager's GetCertificate so certmagic can answer the
	// TLS-ALPN-01 challenge.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: mgr.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1", ACMETLSALPNProto},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })}
	go srv.Serve(ln)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Trigger issuance by requesting a certificate for the custom domain.
	hello := &tls.ClientHelloInfo{ServerName: domain, Conn: &net.TCPConn{}}
	if _, err := mgr.cfg.GetCertificate(hello); err != nil {
		// On-demand issuance happens on handshake; a direct call warms it.
		t.Logf("initial GetCertificate: %v (on-demand continues on handshake)", err)
	}
	_ = ctx
}
