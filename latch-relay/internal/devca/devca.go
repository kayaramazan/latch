// Package devca creates a throwaway certificate authority for development
// and tests. Agents trust it explicitly via --ca-file; certificate
// verification is never disabled.
package devca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Files are the paths Init writes.
type Files struct {
	CACert string // ca.pem
	CAKey  string // ca.key
	Cert   string // server.pem
	Key    string // server.key
}

// Paths returns the file layout inside dir.
func Paths(dir string) Files {
	return Files{
		CACert: filepath.Join(dir, "ca.pem"),
		CAKey:  filepath.Join(dir, "ca.key"),
		Cert:   filepath.Join(dir, "server.pem"),
		Key:    filepath.Join(dir, "server.key"),
	}
}

// Init creates a CA (if missing) and a server certificate for hosts
// (DNS names, wildcards like *.relay.local, or IPs) in dir. It is safe to
// call again: the CA is reused and the server certificate re-issued.
func Init(dir string, hosts []string, validFor time.Duration) (Files, error) {
	if len(hosts) == 0 {
		return Files{}, fmt.Errorf("devca: at least one host is required")
	}
	if validFor == 0 {
		validFor = 365 * 24 * time.Hour
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Files{}, err
	}
	f := Paths(dir)

	caCert, caKey, err := loadOrCreateCA(f, validFor)
	if err != nil {
		return Files{}, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Files{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"Latch dev"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, strings.ToLower(h))
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return Files{}, err
	}
	if err := writePEM(f.Cert, "CERTIFICATE", der); err != nil {
		return Files{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Files{}, err
	}
	if err := writePEM(f.Key, "EC PRIVATE KEY", keyDER); err != nil {
		return Files{}, err
	}
	return f, nil
}

func loadOrCreateCA(f Files, validFor time.Duration) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if certPEM, err := os.ReadFile(f.CACert); err == nil {
		keyPEM, err := os.ReadFile(f.CAKey)
		if err != nil {
			return nil, nil, fmt.Errorf("devca: ca.pem exists but ca.key is unreadable: %w", err)
		}
		cb, _ := pem.Decode(certPEM)
		kb, _ := pem.Decode(keyPEM)
		if cb == nil || kb == nil {
			return nil, nil, fmt.Errorf("devca: existing CA files are not PEM")
		}
		cert, err := x509.ParseCertificate(cb.Bytes)
		if err != nil {
			return nil, nil, err
		}
		key, err := x509.ParseECPrivateKey(kb.Bytes)
		if err != nil {
			return nil, nil, err
		}
		return cert, key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Latch dev CA", Organization: []string{"Latch dev"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validFor * 2),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(f.CACert, "CERTIFICATE", der); err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(f.CAKey, "EC PRIVATE KEY", keyDER); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

func writePEM(path, typ string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600)
}

// LoadServerCert loads server.pem/server.key as a tls.Certificate.
func LoadServerCert(f Files) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(f.Cert, f.Key)
}

// CAPool returns a pool containing the CA, for clients.
func CAPool(caFile string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("devca: %s contains no certificates", caFile)
	}
	return pool, nil
}
