// Package relay wires store, auth, registry, sessions and ingress into one
// runnable relay with its three listeners (public HTTPS, HTTP redirect,
// agent tunnel port).
package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	"github.com/kayaramazan/latch/latch-relay/internal/control"
	"github.com/kayaramazan/latch/latch-relay/internal/ingress"
	"github.com/kayaramazan/latch/latch-relay/internal/metrics"
	"github.com/kayaramazan/latch/latch-relay/internal/policies"
	"github.com/kayaramazan/latch/latch-relay/internal/registry"
	"github.com/kayaramazan/latch/latch-relay/internal/session"
	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/latch-relay/internal/usage"
	pcontrol "github.com/kayaramazan/latch/proto/control"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// CertProvider returns the certificate for a TLS handshake. Phase 2 uses a
// static certificate; phase 4 plugs in certmagic.
type CertProvider func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// StaticCert returns a CertProvider for one certificate.
func StaticCert(c tls.Certificate) CertProvider {
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &c, nil }
}

// Config configures a Relay.
type Config struct {
	DataDir    string
	PublicHost string // the relay's own hostname: tcp:// URLs, connection strings
	BaseDomain string // wildcard zone for generated subdomains (default PublicHost)

	HTTPSAddr string // default ":443"
	HTTPAddr  string // default ":80"; "" disables the redirect listener
	AgentAddr string // default ":7443"

	TCPBindHost  string
	TCPPortRange [2]int // default 20000-20999

	// GeoIPPath is an optional MaxMind mmdb for country rules.
	GeoIPPath string
	// TrustedProxies are CIDRs whose X-Forwarded-For is trusted for client IP.
	TrustedProxies []string
	// MetricsAddr is the Prometheus /metrics listen address ("" disables).
	MetricsAddr string
	// PublicIP is the relay's public IP (for custom-domain A-record checks).
	PublicIP string

	// Control configures the private, mTLS, flag-gated SaaS control API. It is
	// off unless ControlEnabled is set. Self-hosted relays leave it off.
	ControlEnabled    bool
	ControlAddr       string // private listen address, e.g. 127.0.0.1:7444
	ControlServerCert string // PEM file for the control listener
	ControlServerKey  string
	ControlClientCA   string // PEM file: the control plane's CA (verifies clients)
	RelayID           string
	Region            string
	ControlPlaneURL   string // where to push heartbeats ("" disables push)
	ControlRegToken   string // bearer token for heartbeat auth
	Capacity          int

	Certs CertProvider
	// ACMEALPNChallenge adds the "acme-tls/1" ALPN to the HTTPS listener so the
	// certificate provider (certmagic) can answer TLS-ALPN-01 challenges for
	// on-demand custom domains.
	ACMEALPNChallenge bool
	Version           string
	Logger            *slog.Logger
	OnEvent           func(model.AccessEvent)
	// RevocationCheck is how often live sessions are re-checked against the
	// store (token revoked/expired by the CLI). Default 30s.
	RevocationCheck time.Duration
}

func (c *Config) defaults() error {
	if c.DataDir == "" {
		return errors.New("relay: DataDir is required")
	}
	if c.PublicHost == "" {
		return errors.New("relay: PublicHost is required")
	}
	if c.BaseDomain == "" {
		c.BaseDomain = c.PublicHost
	}
	if c.HTTPSAddr == "" {
		c.HTTPSAddr = ":443"
	}
	if c.AgentAddr == "" {
		c.AgentAddr = ":7443"
	}
	if c.TCPPortRange == [2]int{} {
		c.TCPPortRange = [2]int{20000, 20999}
	}
	if c.TCPPortRange[0] < 1 || c.TCPPortRange[1] > 65535 || c.TCPPortRange[0] > c.TCPPortRange[1] {
		return fmt.Errorf("relay: invalid tcp port range %d-%d", c.TCPPortRange[0], c.TCPPortRange[1])
	}
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.RevocationCheck == 0 {
		c.RevocationCheck = 30 * time.Second
	}
	return nil
}

// tunnelDefaultAgentPort mirrors tunnel.DefaultAgentPort.
const tunnelDefaultAgentPort = 7443

// Relay is a running or startable relay.
type Relay struct {
	cfg      Config
	log      *slog.Logger
	store    *store.Store
	auth     *auth.Manager
	policies *policies.Manager
	metrics  *metrics.Metrics
	usage    *usage.Tracker
	control  *control.Server
	registry *registry.Registry
	sessions *session.Manager
	ingress  *ingress.Ingress

	httpsLn, httpLn, agentLn, metricsLn, controlLn net.Listener
	httpsSrv, httpSrv, metricsSrv                  *http.Server
	cancel                                         context.CancelFunc
	wg                                             sync.WaitGroup
}

// New opens the store and builds all components without listening.
func New(cfg Config) (*Relay, error) {
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "relay.db"))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	_, agentPort, _ := net.SplitHostPort(cfg.AgentAddr)
	for k, v := range map[string]string{
		store.SettingPublicHost: cfg.PublicHost,
		store.SettingAgentPort:  agentPort,
		store.SettingBaseDomain: cfg.BaseDomain,
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	r := &Relay{cfg: cfg, log: cfg.Logger, store: st, auth: auth.New(st)}
	pm, err := policies.New(policies.Options{Store: st, TrustedProxies: cfg.TrustedProxies, GeoIPPath: cfg.GeoIPPath, Logger: cfg.Logger})
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	r.policies = pm
	r.metrics = metrics.New()
	r.usage = usage.New()
	_, httpsPortStr, _ := net.SplitHostPort(cfg.HTTPSAddr)
	httpsPort, _ := strconv.Atoi(httpsPortStr)
	if httpsPort == 0 {
		httpsPort = 443
	}
	onEvent := func(ev model.AccessEvent) {
		r.metrics.Record(ev)
		r.usage.Record(ev)
		if cfg.OnEvent != nil {
			cfg.OnEvent(ev)
		}
	}
	r.ingress = ingress.New(ingress.Options{Logger: cfg.Logger, OnEvent: onEvent, TCPBindHost: cfg.TCPBindHost,
		HTTPSPort: httpsPort, Policy: pm, TrustedProxies: cfg.TrustedProxies,
		OnAgentEvent: func(peer registry.Peer, ev model.AccessEvent) {
			if s, ok := peer.(*session.Session); ok {
				s.SendEvent(ev)
			}
		}})
	r.registry = registry.New(st, registry.Options{PublicHost: cfg.PublicHost, HTTPSPort: httpsPort,
		TCPPortRange: cfg.TCPPortRange, OnTCPChange: r.ingress.OnTCPChange})
	r.ingress.SetRegistry(r.registry)
	caps := []string{tunnel.CapTCP, tunnel.CapEvents}
	if cfg.GeoIPPath != "" {
		caps = append(caps, tunnel.CapGeoIP)
	}
	r.sessions = session.NewManager(session.Deps{Auth: r.auth, Registry: r.registry, RelayVersion: cfg.Version,
		BaseDomain: cfg.BaseDomain, TCPPortRange: cfg.TCPPortRange, Capabilities: caps, Logger: cfg.Logger,
		OnPolicySync: func(s *session.Session, ps *tunnel.PolicySync) (tunnel.PolicyAck, error) {
			version, geoMissing, err := r.policies.Sync(context.Background(), s.TokenID(), ps)
			if err != nil {
				return tunnel.PolicyAck{}, err
			}
			if geoMissing {
				r.log.Warn("policy uses country rules but no GeoIP database is configured", "token", s.TokenID())
			}
			return tunnel.PolicyAck{Version: version}, nil
		}})
	if cfg.ControlEnabled {
		_, agentPortStr, _ := net.SplitHostPort(cfg.AgentAddr)
		agentPort, _ := strconv.Atoi(agentPortStr)
		if agentPort == 0 {
			agentPort = tunnelDefaultAgentPort
		}
		r.control = control.New(control.Deps{
			Auth: r.auth, Store: st, Sessions: r.sessions, Registry: r.registry, Usage: r.usage,
			RelayID: cfg.RelayID, Region: cfg.Region, Version: cfg.Version,
			PublicHost: cfg.PublicHost, BaseDomain: cfg.BaseDomain, AgentPort: agentPort,
			PublicIP: cfg.PublicIP, Logger: cfg.Logger,
		})
	}
	return r, nil
}

// DataDir returns the configured data directory.
func (r *Relay) DataDir() string { return r.cfg.DataDir }

// Store, Auth, Registry and Sessions expose components for the CLI and tests.
func (r *Relay) Store() *store.Store          { return r.store }
func (r *Relay) Auth() *auth.Manager          { return r.auth }
func (r *Relay) Registry() *registry.Registry { return r.registry }
func (r *Relay) Sessions() *session.Manager   { return r.sessions }

// Addrs returns the bound listener addresses (https, http, agent); http is
// nil when the redirect listener is disabled.
func (r *Relay) Addrs() (https, httpAddr, agent net.Addr) {
	if r.httpsLn != nil {
		https = r.httpsLn.Addr()
	}
	if r.httpLn != nil {
		httpAddr = r.httpLn.Addr()
	}
	if r.agentLn != nil {
		agent = r.agentLn.Addr()
	}
	return
}

// Start binds the listeners and serves until Stop. It returns after the
// listeners are bound.
// SetCertProvider sets the TLS certificate source and whether the HTTPS
// listener must offer the ACME TLS-ALPN challenge protocol. It must be called
// before Start. This lets the caller build a certificate manager that depends
// on the relay's store (which New creates), then wire it back in.
func (r *Relay) SetCertProvider(c CertProvider, acmeALPN bool) {
	r.cfg.Certs = c
	r.cfg.ACMEALPNChallenge = acmeALPN
}

func (r *Relay) Start(ctx context.Context) error {
	if r.cfg.Certs == nil {
		return errors.New("relay: no certificate provider configured (use --dev, --tls-cert/--tls-key, or ACME)")
	}
	ctx, r.cancel = context.WithCancel(ctx)
	if err := r.registry.Load(ctx); err != nil {
		return fmt.Errorf("relay: load routes: %w", err)
	}
	if err := r.policies.Load(ctx); err != nil {
		return fmt.Errorf("relay: load policies: %w", err)
	}

	httpsNextProtos := []string{"h2", "http/1.1"}
	if r.cfg.ACMEALPNChallenge {
		httpsNextProtos = append(httpsNextProtos, "acme-tls/1")
	}
	httpsTLS := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     httpsNextProtos,
		GetCertificate: r.cfg.Certs,
	}
	ln, err := net.Listen("tcp", r.cfg.HTTPSAddr)
	if err != nil {
		return fmt.Errorf("relay: listen https %s: %w", r.cfg.HTTPSAddr, err)
	}
	r.httpsLn = ln
	if _, p, _ := net.SplitHostPort(r.cfg.HTTPSAddr); p == "0" || p == "" {
		actual := ln.Addr().(*net.TCPAddr).Port
		r.registry.SetHTTPSPort(actual)
		r.ingress.SetHTTPSPort(actual)
	}
	r.httpsSrv = ingress.NewHTTPServer(r.ingress, r.log)
	r.httpsSrv.TLSConfig = httpsTLS
	httpsListener := tls.NewListener(ingress.LimitListener(ln, 0, 0), httpsTLS)
	r.serve(func() { _ = r.httpsSrv.Serve(httpsListener) })

	if r.cfg.HTTPAddr != "" {
		ln, err := net.Listen("tcp", r.cfg.HTTPAddr)
		if err != nil {
			r.closeListeners()
			return fmt.Errorf("relay: listen http %s: %w", r.cfg.HTTPAddr, err)
		}
		r.httpLn = ln
		r.httpSrv = ingress.NewHTTPServer(r.ingress.RedirectHandler(), r.log)
		r.serve(func() { _ = r.httpSrv.Serve(ingress.LimitListener(ln, 0, 0)) })
	}

	agentTLS := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{tunnel.ALPN},
		GetCertificate: r.cfg.Certs,
	}
	aln, err := net.Listen("tcp", r.cfg.AgentAddr)
	if err != nil {
		r.closeListeners()
		return fmt.Errorf("relay: listen agent %s: %w", r.cfg.AgentAddr, err)
	}
	r.agentLn = aln
	r.serve(func() { r.acceptAgents(ctx, tls.NewListener(ingress.LimitListener(aln, 0, 0), agentTLS)) })
	r.serve(func() { r.watchRevocations(ctx) })

	if r.cfg.MetricsAddr != "" {
		mln, err := net.Listen("tcp", r.cfg.MetricsAddr)
		if err != nil {
			r.closeListeners()
			return fmt.Errorf("relay: listen metrics %s: %w", r.cfg.MetricsAddr, err)
		}
		r.metricsLn = mln
		mux := http.NewServeMux()
		mux.Handle("/metrics", r.metrics.Handler())
		r.metricsSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		r.serve(func() { _ = r.metricsSrv.Serve(mln) })
		r.log.Info("metrics listening", "addr", mln.Addr())
	}
	r.serve(func() { r.updateGauges(ctx) })

	if r.cfg.ControlEnabled {
		if err := r.startControl(ctx); err != nil {
			r.closeListeners()
			return err
		}
	}

	r.log.Info("relay listening", "https", r.httpsLn.Addr(), "agent", r.agentLn.Addr(), "publicHost", r.cfg.PublicHost, "version", r.cfg.Version)
	return nil
}

func (r *Relay) serve(fn func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

func (r *Relay) closeListeners() {
	for _, l := range []net.Listener{r.httpsLn, r.httpLn, r.agentLn, r.metricsLn, r.controlLn} {
		if l != nil {
			_ = l.Close()
		}
	}
}

func (r *Relay) acceptAgents(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			tc, ok := conn.(*tls.Conn)
			if !ok {
				_ = conn.Close()
				return
			}
			hctx, cancel := context.WithTimeout(ctx, tunnel.HelloTimeout)
			err := tc.HandshakeContext(hctx)
			cancel()
			if err != nil {
				r.log.Debug("agent tls handshake failed", "remote", conn.RemoteAddr(), "err", err)
				_ = conn.Close()
				return
			}
			if tc.ConnectionState().NegotiatedProtocol != tunnel.ALPN {
				r.log.Debug("agent connection without latch ALPN", "remote", conn.RemoteAddr())
				_ = conn.Close()
				return
			}
			r.sessions.Serve(ctx, tc)
		}()
	}
}

// watchRevocations closes sessions whose token was revoked or expired via
// the CLI (a separate process) since they connected.
// updateGauges refreshes live Prometheus gauges.
func (r *Relay) updateGauges(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.metrics.SetAgents(len(r.sessions.Sessions()))
		}
	}
}

// startControl brings up the mTLS control listener and the heartbeat pusher.
func (r *Relay) startControl(ctx context.Context) error {
	if r.cfg.ControlServerCert == "" || r.cfg.ControlServerKey == "" || r.cfg.ControlClientCA == "" {
		return errors.New("relay: control API needs --control-cert, --control-key and --control-client-ca")
	}
	cert, err := tls.LoadX509KeyPair(r.cfg.ControlServerCert, r.cfg.ControlServerKey)
	if err != nil {
		return fmt.Errorf("relay: control cert: %w", err)
	}
	caPEM, err := os.ReadFile(r.cfg.ControlClientCA)
	if err != nil {
		return fmt.Errorf("relay: control client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("relay: control client CA is not a valid certificate")
	}
	addr := r.cfg.ControlAddr
	if addr == "" {
		addr = "127.0.0.1:7444"
	}
	ln, err := tls.Listen("tcp", addr, control.MTLSConfig(cert, pool))
	if err != nil {
		return fmt.Errorf("relay: listen control %s: %w", addr, err)
	}
	r.controlLn = ln
	r.serve(func() { _ = r.control.Serve(ctx, ln) })
	if r.cfg.ControlPlaneURL != "" {
		r.serve(func() { r.heartbeatLoop(ctx) })
	}
	return nil
}

// heartbeatLoop pushes register+liveness+usage to the control plane.
func (r *Relay) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	r.sendHeartbeat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sendHeartbeat(ctx)
		}
	}
}

func (r *Relay) sendHeartbeat(ctx context.Context) {
	hb := pcontrol.Heartbeat{
		RelayID: r.cfg.RelayID, Region: r.cfg.Region, Version: r.cfg.Version,
		ControlURL: r.cfg.ControlAddr, Capacity: r.cfg.Capacity, Health: r.control.Health(ctx),
	}
	body, _ := json.Marshal(hb)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.ControlPlaneURL+"/relays/heartbeat", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.ControlRegToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.ControlRegToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.log.Debug("heartbeat failed", "err", err)
		return
	}
	_ = resp.Body.Close()
}

func (r *Relay) watchRevocations(ctx context.Context) {
	t := time.NewTicker(r.cfg.RevocationCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			seen := map[string]bool{}
			for _, s := range r.sessions.Sessions() {
				if seen[s.TokenID] {
					continue
				}
				seen[s.TokenID] = true
				tok, err := r.store.Token(ctx, s.TokenID)
				if err != nil {
					continue
				}
				if !tok.Active(time.Now()) {
					r.log.Info("token no longer active, closing its sessions", "token", tok.Name)
					r.registry.DropToken(tok.ID)
					r.policies.Drop(ctx, tok.ID)
					r.sessions.CloseToken(tok.ID, tunnel.ByeTokenRevoked)
				}
			}
		}
	}
}

// RevokeToken revokes a token and closes its sessions immediately.
func (r *Relay) RevokeToken(ctx context.Context, id string) error {
	if err := r.auth.Revoke(ctx, id); err != nil {
		return err
	}
	r.registry.DropToken(id)
	r.sessions.CloseToken(id, tunnel.ByeTokenRevoked)
	return nil
}

// Stop shuts everything down gracefully.
func (r *Relay) Stop(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.sessions.Shutdown()
	r.ingress.Close()
	for _, srv := range []*http.Server{r.httpsSrv, r.httpSrv, r.metricsSrv} {
		if srv != nil {
			_ = srv.Shutdown(ctx)
		}
	}
	r.closeListeners()
	r.wg.Wait()
	_ = r.policies.Close()
	return r.store.Close()
}
