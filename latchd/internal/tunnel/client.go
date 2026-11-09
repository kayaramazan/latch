package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// State is a relay client's connection state.
type State string

// Client states, per ROADMAP phase 3.
const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateBackoff    State = "backoff"
	StateStopped    State = "stopped"
)

// Backoff bounds for reconnection (exponential with jitter).
const (
	BackoffMin    = 1 * time.Second
	BackoffMax    = 60 * time.Second
	BackoffFactor = 2.0
	dialTimeout   = 10 * time.Second
	localDialWait = tunnel.StreamOpenTimeout
)

// ClientStatus is a snapshot of one relay client for the management API.
type ClientStatus struct {
	RelayID       string    `json:"relayId"`
	RelayName     string    `json:"relayName"`
	Addr          string    `json:"addr"`
	State         State     `json:"state"`
	ConnectedAt   time.Time `json:"connectedAt,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	BaseDomain    string    `json:"baseDomain,omitempty"`
	RTTMs         float64   `json:"rttMs"`
	Reconnects    int64     `json:"reconnects"`
	Routes        int       `json:"routes"`
	Accepted      []string  `json:"accepted,omitempty"`
	Rejected      []string  `json:"rejected,omitempty"`
	PublicURLs    []string  `json:"publicUrls,omitempty"`
	StreamsTotal  int64     `json:"streamsTotal"`
	BytesIn       int64     `json:"bytesIn"`
	BytesOut      int64     `json:"bytesOut"`
	SyncState     string    `json:"syncState"`
	PolicyVersion int64     `json:"policyVersion"`
}

// Client maintains one authenticated tunnel to a single relay, reconnecting
// with exponential backoff. It is safe for concurrent use.
type Client struct {
	relay        model.Relay
	agentID      string
	agentVersion string
	log          *slog.Logger

	events EventSink

	mu        sync.Mutex
	specs     []tunnel.RouteSpec
	targets   map[string]string
	policy    tunnel.PolicySync
	hasPolicy bool
	active    *conn

	// status fields
	state       atomic.Value // State
	connectedAt atomic.Int64 // unix nanos, 0 when not connected
	lastErr     atomic.Value // string
	baseDomain  atomic.Value // string
	rttNs       atomic.Int64
	reconnects  atomic.Int64
	policyVer   atomic.Int64
	ackedPolicy atomic.Int64
	streams     atomic.Int64
	bytesIn     atomic.Int64 // from relay (request bytes to local)
	bytesOut    atomic.Int64 // to relay (response bytes from local)
	accepted    atomic.Value // []string hostnames/ports
	rejected    atomic.Value // []string "routeID: reason"
	publicURLs  atomic.Value // []string
}

// NewClient builds a client for one relay. Call SetRoutes before or after
// Run; Run blocks until ctx is cancelled.
func NewClient(relay model.Relay, agentID, agentVersion string, events EventSink, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	c := &Client{
		relay: relay, agentID: agentID, agentVersion: agentVersion,
		events:  events,
		targets: map[string]string{},
		log:     log.With("relay", relay.Name, "relayId", relay.ID),
	}
	c.state.Store(StateConnecting)
	return c
}

// RelayID returns the relay this client serves.
func (c *Client) RelayID() string { return c.relay.ID }

// SetRoutes replaces the full set of tunnel routes this client announces. If
// connected, it re-announces immediately (the relay treats RouteAnnounce as
// the full set, so this also withdraws routes no longer present).
func (c *Client) SetRoutes(specs []tunnel.RouteSpec, targets map[string]string) {
	c.mu.Lock()
	c.specs = specs
	c.targets = targets
	active := c.active
	c.mu.Unlock()
	if active != nil {
		active.announce(specs)
	}
}

// SetPolicy replaces the full policy snapshot this client sends to the relay.
// If connected, it syncs immediately. version should increase monotonically.
func (c *Client) SetPolicy(ps tunnel.PolicySync) {
	c.mu.Lock()
	c.policy = ps
	c.hasPolicy = true
	active := c.active
	c.mu.Unlock()
	c.policyVer.Store(int64(ps.Version))
	if active != nil {
		active.syncPolicy(ps)
	}
}

func (c *Client) setState(s State) { c.state.Store(s) }

func (c *Client) target(routeID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.targets[routeID]
}

// Run connects and keeps the tunnel alive until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	backoff := BackoffMin
	for {
		if ctx.Err() != nil {
			c.setState(StateStopped)
			return
		}
		c.setState(StateConnecting)
		cn, fatal, err := c.connect(ctx)
		if err != nil {
			c.lastErr.Store(err.Error())
			if fatal {
				c.log.Error("tunnel handshake refused by relay", "err", err)
			} else {
				c.log.Warn("tunnel connect failed", "err", err, "retryIn", backoff)
			}
			c.setState(StateBackoff)
			if !sleepCtx(ctx, jitter(backoff)) {
				c.setState(StateStopped)
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		// Connected.
		backoff = BackoffMin
		c.reconnects.Add(1)
		c.connectedAt.Store(time.Now().UnixNano())
		c.baseDomain.Store(cn.ack.BaseDomain)
		c.lastErr.Store("")
		c.setState(StateConnected)
		c.log.Info("tunnel connected", "relayVersion", cn.ack.RelayVersion, "baseDomain", cn.ack.BaseDomain)

		c.mu.Lock()
		c.active = cn
		specs := c.specs
		ps := c.policy
		hasPolicy := c.hasPolicy
		c.mu.Unlock()
		cn.announce(specs)
		if hasPolicy {
			cn.syncPolicy(ps)
		}

		cn.serve(ctx)

		c.mu.Lock()
		c.active = nil
		c.mu.Unlock()
		c.connectedAt.Store(0)
		if reason := cn.closeReason(); reason != "" {
			c.lastErr.Store(reason)
		}
		c.log.Info("tunnel disconnected", "reason", cn.closeReason())
		c.setState(StateBackoff)
		if !sleepCtx(ctx, jitter(backoff)) {
			c.setState(StateStopped)
			return
		}
	}
}

// connect dials the relay, performs the handshake and returns a live conn.
// fatal is true when the relay refused us (auth/version); the caller still
// retries, but logs it as an error.
func (c *Client) connect(ctx context.Context) (cn *conn, fatal bool, err error) {
	cfg, err := c.tlsConfig()
	if err != nil {
		return nil, true, err
	}
	addr := net.JoinHostPort(c.relay.Host, fmt.Sprint(c.relayPort()))
	d := &net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, false, fmt.Errorf("dial %s: %w", addr, err)
	}
	tconn := tls.Client(raw, cfg)
	hsCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := tconn.HandshakeContext(hsCtx); err != nil {
		_ = raw.Close()
		return nil, false, fmt.Errorf("tls handshake: %w", err)
	}
	mux, err := yamux.Client(tconn, tunnel.YamuxConfig())
	if err != nil {
		_ = tconn.Close()
		return nil, false, fmt.Errorf("yamux: %w", err)
	}
	ctrlStream, err := mux.OpenStream()
	if err != nil {
		_ = mux.Close()
		return nil, false, fmt.Errorf("open control stream: %w", err)
	}
	cn = &conn{c: c, mux: mux, tconn: tconn, ctrl: tunnel.NewCodec(ctrlStream), done: make(chan struct{})}
	if fatal, err := cn.handshake(); err != nil {
		_ = mux.Close()
		_ = tconn.Close()
		return nil, fatal, err
	}
	return cn, false, nil
}

func (c *Client) relayPort() int {
	if c.relay.Port == 0 {
		return tunnel.DefaultAgentPort
	}
	return c.relay.Port
}

// tlsConfig builds a verifying TLS config: system roots plus the relay's
// optional dev CA. Certificate verification is never skipped.
func (c *Client) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		ServerName: c.relay.Host,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{tunnel.ALPN},
	}
	if c.relay.CAPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(c.relay.CAPEM)) {
			return nil, errors.New("relay caPem is not a valid certificate")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// Status returns a snapshot for the management API.
func (c *Client) Status() ClientStatus {
	st, _ := c.state.Load().(State)
	s := ClientStatus{
		RelayID: c.relay.ID, RelayName: c.relay.Name,
		Addr:         net.JoinHostPort(c.relay.Host, fmt.Sprint(c.relayPort())),
		State:        st,
		Reconnects:   c.reconnects.Load() - 1, // first connect is not a reconnect
		StreamsTotal: c.streams.Load(),
		BytesIn:      c.bytesIn.Load(),
		BytesOut:     c.bytesOut.Load(),
		RTTMs:        float64(c.rttNs.Load()) / 1e6,
	}
	if s.Reconnects < 0 {
		s.Reconnects = 0
	}
	if ts := c.connectedAt.Load(); ts != 0 {
		s.ConnectedAt = time.Unix(0, ts).UTC()
	}
	if v, ok := c.lastErr.Load().(string); ok {
		s.LastError = v
	}
	if v, ok := c.baseDomain.Load().(string); ok {
		s.BaseDomain = v
	}
	if v, ok := c.accepted.Load().([]string); ok {
		s.Accepted = v
	}
	if v, ok := c.rejected.Load().([]string); ok {
		s.Rejected = v
	}
	if v, ok := c.publicURLs.Load().([]string); ok {
		s.PublicURLs = v
	}
	c.mu.Lock()
	s.Routes = len(c.specs)
	hasPolicy := c.hasPolicy
	c.mu.Unlock()
	s.PolicyVersion = c.policyVer.Load()
	switch {
	case !hasPolicy:
		s.SyncState = "n/a"
	case st == StateConnected && c.ackedPolicy.Load() == c.policyVer.Load():
		s.SyncState = "in_sync"
	default:
		s.SyncState = "out_of_sync"
	}
	return s
}

// --- helpers ---

func nextBackoff(d time.Duration) time.Duration {
	n := time.Duration(float64(d) * BackoffFactor)
	if n > BackoffMax {
		return BackoffMax
	}
	return n
}

// jitter returns d scaled by a random factor in [0.8, 1.2].
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// sleepCtx sleeps for d or until ctx is done; it returns false if ctx ended.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
