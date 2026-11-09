package devca

import (
	"crypto/x509"
	"os"
	"testing"
	"time"
)

func TestInitAndReuse(t *testing.T) {
	dir := t.TempDir()
	f, err := Init(dir, []string{"relay.local", "*.relay.local", "127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := CAPool(f.CACert)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := LoadServerCert(f)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	for _, name := range []string{"relay.local", "app.relay.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Errorf("verify %s: %v", name, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "other.example.com", Roots: pool}); err == nil {
		t.Error("unrelated name verified")
	}
	if len(leaf.IPAddresses) != 1 {
		t.Error("IP SAN missing")
	}
	ca1, _ := os.ReadFile(f.CACert)

	if _, err := Init(dir, []string{"relay.local"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	ca2, _ := os.ReadFile(f.CACert)
	if string(ca1) != string(ca2) {
		t.Error("CA was regenerated instead of reused")
	}
	info, _ := os.Stat(f.CAKey)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key perm %o", info.Mode().Perm())
	}
	if _, err := Init(dir, nil, 0); err == nil {
		t.Error("no hosts accepted")
	}
}
