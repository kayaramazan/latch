package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	"github.com/mholt/acmez/v3"
	"go.uber.org/zap"
)

// DNS provider identifiers.
const (
	DNSCloudflare = "cloudflare"
	DNSManual     = "manual"
)

// ACMETLSALPNProto is the ALPN protocol the relay's HTTPS listener must offer
// so certmagic can answer TLS-ALPN-01 challenges for custom domains.
const ACMETLSALPNProto = "acme-tls/1"

// Options configure the certificate Manager.
type Options struct {
	DataDir    string
	Email      string
	BaseDomain string // wildcard zone, e.g. relay.example.com
	PublicHost string // the relay's own hostname (CNAME target for custom domains)
	PublicIP   string // the relay's public IP (A-record target), optional

	Staging  bool   // use Let's Encrypt staging
	CADirURL string // override the ACME directory URL (e.g. Pebble); wins over Staging

	DNSProvider     string // "cloudflare" or "manual"
	CloudflareToken string
	ManualHooks     ManualHooks

	Domains      DomainChecker
	Resolver     Resolver
	TrustedRoots *x509.CertPool // extra roots to trust for the ACME CA (Pebble)
	Logger       *slog.Logger
}

// Manager owns the relay's certificate configuration.
type Manager struct {
	cfg   *certmagic.Config
	cache *certmagic.Cache

	baseDomain string
	publicHost string
	publicIP   string
	domains    DomainChecker
	resolver   Resolver
	log        *slog.Logger
}

// New builds a Manager. It does not perform any network I/O; call Start to
// begin managing the wildcard certificate.
func New(opts Options) (*Manager, error) {
	if opts.DataDir == "" {
		return nil, errors.New("acme: DataDir is required")
	}
	if opts.Email == "" {
		return nil, errors.New("acme: Email is required")
	}
	if opts.BaseDomain == "" {
		return nil, errors.New("acme: BaseDomain is required")
	}
	if opts.PublicHost == "" {
		return nil, errors.New("acme: PublicHost is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Resolver == nil {
		opts.Resolver = systemResolver{r: net.DefaultResolver}
	}

	m := &Manager{
		baseDomain: normalizeName(opts.BaseDomain),
		publicHost: normalizeName(opts.PublicHost),
		publicIP:   opts.PublicIP,
		domains:    opts.Domains,
		resolver:   opts.Resolver,
		log:        opts.Logger,
	}

	storage := &certmagic.FileStorage{Path: filepath.Join(opts.DataDir, "acme")}
	var cfg *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return cfg, nil },
	})
	cfg = certmagic.New(cache, certmagic.Config{
		Storage:           storage,
		Logger:            zap.NewNop(),
		DefaultServerName: m.publicHost,
		OnDemand:          &certmagic.OnDemandConfig{DecisionFunc: m.decide},
	})

	dnsSolver, err := buildDNSSolver(opts, m.baseDomain)
	if err != nil {
		return nil, err
	}

	ca := certmagic.LetsEncryptProductionCA
	if opts.Staging {
		ca = certmagic.LetsEncryptStagingCA
	}
	if opts.CADirURL != "" {
		ca = opts.CADirURL
	}
	base := certmagic.ACMEIssuer{
		CA:                   ca,
		Email:                opts.Email,
		Agreed:               true,
		DisableHTTPChallenge: true, // the relay does not serve HTTP-01
		TrustedRoots:         opts.TrustedRoots,
	}

	// Wildcard / base-domain issuer: DNS-01 only.
	dnsTmpl := base
	dnsTmpl.DNS01Solver = dnsSolver
	dnsTmpl.DisableTLSALPNChallenge = true
	dnsIssuer := certmagic.NewACMEIssuer(cfg, dnsTmpl)

	// Custom-domain issuer: TLS-ALPN-01 (no DNS access to the customer's zone).
	alpnTmpl := base
	alpnTmpl.DisableTLSALPNChallenge = false
	alpnIssuer := certmagic.NewACMEIssuer(cfg, alpnTmpl)

	cfg.Issuers = []certmagic.Issuer{dnsIssuer, alpnIssuer}

	m.cfg = cfg
	m.cache = cache
	return m, nil
}

func buildDNSSolver(opts Options, baseDomain string) (acmez.Solver, error) {
	switch opts.DNSProvider {
	case DNSCloudflare:
		if opts.CloudflareToken == "" {
			return nil, errors.New("acme: cloudflare DNS requires an API token")
		}
		return &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{DNSProvider: &cloudflare.Provider{APIToken: opts.CloudflareToken}},
		}, nil
	case DNSManual, "":
		hooks := opts.ManualHooks
		hooks.BaseDomain = baseDomain
		return &manualSolver{hooks: hooks}, nil
	default:
		return nil, fmt.Errorf("acme: unknown DNS provider %q", opts.DNSProvider)
	}
}

// Start begins managing the wildcard certificate for the base domain in the
// background. Certificates are obtained/renewed asynchronously.
func (m *Manager) Start(ctx context.Context) error {
	names := []string{"*." + m.baseDomain, m.baseDomain}
	m.log.Info("managing wildcard certificate", "names", names)
	return m.cfg.ManageAsync(ctx, names)
}

// GetCertificate is the tls.Config.GetCertificate callback. It serves managed
// and on-demand certificates and answers ACME TLS-ALPN-01 challenges.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.cfg.GetCertificate(hello)
}

// Stop releases the certificate cache.
func (m *Manager) Stop() {
	if m.cache != nil {
		m.cache.Stop()
	}
}
