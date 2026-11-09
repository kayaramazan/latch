// Package session runs the relay side of one agent connection: handshake,
// control-stream message loop, ping/pong liveness, data stream opening and
// the per-token session limits from PROTOCOL.md.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/kayaramazan/latch/latch-relay/internal/auth"
	"github.com/kayaramazan/latch/latch-relay/internal/registry"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Limits from PROTOCOL.md.
const (
	DefaultMaxSessionsPerToken = 4
	MaxRoutesPerSession        = 256
	MaxStreamsPerSession       = 4096
	MaxControlMessagesPerSec   = 50

	// EventBufferSize bounds buffered AccessEvents per session; excess is
	// dropped and counted (reported in the batch).
	EventBufferSize = 1024
	// eventFlushInterval and eventBatchMax bound how AccessEvents are batched.
	eventFlushInterval = 500 * time.Millisecond
	eventBatchMax      = 128
)

// Deps are the collaborators a Manager needs.
type Deps struct {
	Auth         *auth.Manager
	Registry     *registry.Registry
	RelayVersion string
	BaseDomain   string
	TCPPortRange [2]int
	Capabilities []string
	Logger       *slog.Logger
	// MaxSessionsPerToken defaults to DefaultMaxSessionsPerToken.
	MaxSessionsPerToken int
	// OnPolicySync handles PolicySync (phase 5). When nil the relay answers
	// ERR_UNKNOWN_TYPE so the agent reports "not in sync" instead of
	// believing policies are enforced.
	OnPolicySync func(s *Session, ps *tunnel.PolicySync) (tunnel.PolicyAck, error)
}

// Manager owns all live sessions.
type Manager struct {
	deps Deps

	mu       sync.Mutex
	sessions map[string]*Session            // by session id
	byToken  map[string]map[string]*Session // token id → session id → session
	seq      atomic.Uint64
}

// NewManager returns a Manager.
func NewManager(d Deps) *Manager {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.MaxSessionsPerToken <= 0 {
		d.MaxSessionsPerToken = DefaultMaxSessionsPerToken
	}
	if d.Capabilities == nil {
		d.Capabilities = []string{tunnel.CapTCP, tunnel.CapEvents}
	}
	return &Manager{deps: d, sessions: map[string]*Session{}, byToken: map[string]map[string]*Session{}}
}

// Info is a snapshot of one session for status output.
type Info struct {
	ID           string    `json:"id"`
	TokenID      string    `json:"tokenId"`
	TokenName    string    `json:"tokenName"`
	AgentID      string    `json:"agentId"`
	AgentVersion string    `json:"agentVersion"`
	RemoteAddr   string    `json:"remoteAddr"`
	ConnectedAt  time.Time `json:"connectedAt"`
	RTTMs        float64   `json:"rttMs"`
	Streams      int64     `json:"streams"`
}

// Sessions lists live sessions.
func (m *Manager) Sessions() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info())
	}
	return out
}

// CloseToken closes every session of a token (after revocation).
func (m *Manager) CloseToken(tokenID, reason string) {
	m.mu.Lock()
	var list []*Session
	for _, s := range m.byToken[tokenID] {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		s.Close(reason, true)
	}
}

// Shutdown closes all sessions with Goodbye{shutdown}.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	var list []*Session
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		s.Close(tunnel.ByeShutdown, true)
	}
}

// Session is one authenticated agent connection. It implements registry.Peer.
type Session struct {
	m            *Manager
	id           string
	tokenID      string
	tokenName    string
	agentID      string
	agentVersion string
	scope        tunnel.Scope
	proto        int
	conn         net.Conn
	mux          *yamux.Session
	ctrl         *tunnel.Codec
	log          *slog.Logger
	connectedAt  time.Time

	rttNs    atomic.Int64
	lastPong atomic.Int64 // unix nanos
	streams  atomic.Int64

	events     chan model.AccessEvent
	evtDropped atomic.Int64

	closeOnce sync.Once
	done      chan struct{}
	reason    atomic.Value // string
}

// SessionID implements registry.Peer.
func (s *Session) SessionID() string { return s.id }

// TokenID implements registry.Peer.
func (s *Session) TokenID() string { return s.tokenID }

// AgentID implements registry.Peer.
func (s *Session) AgentID() string { return s.agentID }

// Scope returns the token scope.
func (s *Session) Scope() tunnel.Scope { return s.scope }

// Done is closed when the session has ended.
func (s *Session) Done() <-chan struct{} { return s.done }

// CloseReason returns why the session ended ("" while running).
func (s *Session) CloseReason() string {
	v, _ := s.reason.Load().(string)
	return v
}

func (s *Session) info() Info {
	return Info{ID: s.id, TokenID: s.tokenID, TokenName: s.tokenName, AgentID: s.agentID, AgentVersion: s.agentVersion,
		RemoteAddr: s.conn.RemoteAddr().String(), ConnectedAt: s.connectedAt,
		RTTMs: float64(s.rttNs.Load()) / 1e6, Streams: s.streams.Load()}
}

// Send writes a control message.
func (s *Session) Send(t tunnel.Type, re uint64, body any) error {
	_, err := s.ctrl.Send(t, re, body)
	return err
}

// StreamError is returned by OpenStream when the agent refused the stream.
type StreamError struct {
	Status tunnel.Status
}

func (e *StreamError) Error() string { return "tunnel: agent reported " + e.Status.String() }

// ErrTooManyStreams is returned when the per-session stream cap is reached.
var ErrTooManyStreams = errors.New("tunnel: too many streams on session")

// OpenStream implements registry.Peer.
func (s *Session) OpenStream(ctx context.Context, h *tunnel.StreamHeader) (net.Conn, error) {
	if s.streams.Load() >= MaxStreamsPerSession {
		return nil, ErrTooManyStreams
	}
	st, err := s.mux.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("tunnel: open stream: %w", err)
	}
	s.streams.Add(1)
	wrapped := &countedStream{Stream: st, s: s}
	deadline := time.Now().Add(tunnel.StreamOpenTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = st.SetDeadline(deadline)
	if err := tunnel.WriteStreamHeader(st, h); err != nil {
		_ = wrapped.Close()
		return nil, fmt.Errorf("tunnel: write stream header: %w", err)
	}
	status, err := tunnel.ReadStatus(st)
	if err != nil {
		_ = wrapped.Close()
		return nil, fmt.Errorf("tunnel: read stream status: %w", err)
	}
	_ = st.SetDeadline(time.Time{})
	if status != tunnel.StatusOK {
		_ = wrapped.Close()
		return nil, &StreamError{Status: status}
	}
	return wrapped, nil
}

type countedStream struct {
	*yamux.Stream
	s    *Session
	once sync.Once
}

func (c *countedStream) Close() error {
	c.once.Do(func() { c.s.streams.Add(-1) })
	return c.Stream.Close()
}

// Close ends the session. When goodbye is true a Goodbye frame is sent first.
func (s *Session) Close(reason string, goodbye bool) {
	s.closeOnce.Do(func() {
		s.reason.Store(reason)
		if goodbye && s.ctrl != nil {
			_, _ = s.ctrl.Send(tunnel.TypeGoodbye, 0, &tunnel.Goodbye{Reason: reason})
		}
		if s.mux != nil {
			_ = s.mux.Close()
		}
		_ = s.conn.Close()
		close(s.done)
	})
}

// Serve runs a session on an accepted (already TLS) connection until it
// ends. It blocks; call it in its own goroutine.
func (m *Manager) Serve(ctx context.Context, conn net.Conn) {
	s := &Session{m: m, conn: conn, done: make(chan struct{}), connectedAt: time.Now().UTC(),
		events: make(chan model.AccessEvent, EventBufferSize),
		log:    m.deps.Logger.With("remote", conn.RemoteAddr().String())}
	s.id = fmt.Sprintf("ses_%d", m.seq.Add(1))

	mux, err := yamux.Server(conn, tunnel.YamuxConfig())
	if err != nil {
		s.log.Warn("yamux server failed", "err", err)
		_ = conn.Close()
		close(s.done)
		return
	}
	s.mux = mux

	if err := s.handshake(ctx); err != nil {
		s.log.Info("agent handshake failed", "err", err)
		s.Close("handshake_failed", false)
		return
	}
	s.log = s.log.With("session", s.id, "token", s.tokenName, "agent", s.agentID)
	s.log.Info("agent connected", "version", s.agentVersion)

	go s.rejectIncomingStreams()
	go s.pingLoop()
	go s.eventLoop()
	s.readLoop()

	m.deps.Registry.Detach(context.Background(), s)
	m.unregister(s)
	s.Close("connection_lost", false)
	s.log.Info("agent disconnected", "reason", s.CloseReason())
}

func (s *Session) handshake(ctx context.Context) error {
	acceptDone := make(chan struct{})
	var ctrl *yamux.Stream
	var acceptErr error
	go func() {
		ctrl, acceptErr = s.mux.AcceptStream()
		close(acceptDone)
	}()
	select {
	case <-acceptDone:
	case <-time.After(tunnel.HelloTimeout):
		return fmt.Errorf("no control stream within %s", tunnel.HelloTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
	if acceptErr != nil {
		return fmt.Errorf("accept control stream: %w", acceptErr)
	}
	s.ctrl = tunnel.NewCodec(ctrl)
	_ = ctrl.SetReadDeadline(time.Now().Add(tunnel.HelloTimeout))
	env, err := s.ctrl.Recv()
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	_ = ctrl.SetReadDeadline(time.Time{})
	if env.T != tunnel.TypeHello {
		_ = s.ctrl.SendError(env.ID, tunnel.ErrInvalid, "first message must be Hello", true)
		return fmt.Errorf("first message was %s", env.T)
	}
	var hello tunnel.Hello
	if err := env.Decode(&hello); err != nil {
		_ = s.ctrl.SendError(env.ID, tunnel.ErrMalformed, err.Error(), true)
		return err
	}
	proto, ok := tunnel.Negotiate(hello.ProtoMin, hello.ProtoMax)
	if !ok {
		_, _ = s.ctrl.Send(tunnel.TypeError, env.ID, &tunnel.Error{Code: tunnel.ErrVersion, Fatal: true,
			Message:  fmt.Sprintf("agent supports protocol %d-%d, relay supports %d-%d", hello.ProtoMin, hello.ProtoMax, tunnel.MinProtocolVersion, tunnel.ProtocolVersion),
			RelayMin: tunnel.MinProtocolVersion, RelayMax: tunnel.ProtocolVersion})
		return fmt.Errorf("no common protocol version")
	}
	if hello.AgentID == "" {
		_ = s.ctrl.SendError(env.ID, tunnel.ErrInvalid, "agentId is required", true)
		return fmt.Errorf("missing agentId")
	}
	tok, err := s.m.deps.Auth.Verify(ctx, hello.Token)
	if err != nil {
		msg := "token is invalid"
		switch {
		case errors.Is(err, auth.ErrRevoked):
			msg = "token has been revoked"
		case errors.Is(err, auth.ErrExpired):
			msg = "token has expired"
		case !errors.Is(err, auth.ErrInvalidToken):
			_ = s.ctrl.SendError(env.ID, tunnel.ErrInternal, "authentication unavailable", true)
			return err
		}
		_ = s.ctrl.SendError(env.ID, tunnel.ErrAuth, msg, true)
		return fmt.Errorf("auth: %w", err)
	}
	s.proto, s.tokenID, s.tokenName, s.scope = proto, tok.ID, tok.Name, tok.Scope
	s.agentID, s.agentVersion = hello.AgentID, hello.AgentVersion

	if err := s.m.register(s); err != nil {
		_ = s.ctrl.SendError(env.ID, tunnel.ErrTooManySessions, err.Error(), true)
		return err
	}
	s.lastPong.Store(time.Now().UnixNano())
	scope := s.scope
	if scope.TCP && len(scope.TCPPortRange) == 0 {
		scope.TCPPortRange = []int{s.m.deps.TCPPortRange[0], s.m.deps.TCPPortRange[1]}
	}
	_, err = s.ctrl.Send(tunnel.TypeHelloAck, env.ID, &tunnel.HelloAck{
		Proto: proto, RelayVersion: s.m.deps.RelayVersion, ServerTime: time.Now().UTC(),
		Scope: scope, BaseDomain: s.m.deps.BaseDomain, PolicyVersion: 0, Capabilities: s.m.deps.Capabilities,
	})
	return err
}

// register enforces "same token + same agent: newest wins" and the per-token
// session cap.
func (m *Manager) register(s *Session) error {
	m.mu.Lock()
	var supersede *Session
	byTok := m.byToken[s.tokenID]
	if byTok == nil {
		byTok = map[string]*Session{}
		m.byToken[s.tokenID] = byTok
	}
	for _, old := range byTok {
		if old.agentID == s.agentID {
			supersede = old
			delete(byTok, old.id)
			delete(m.sessions, old.id)
			break
		}
	}
	if len(byTok) >= m.deps.MaxSessionsPerToken {
		m.mu.Unlock()
		return fmt.Errorf("token already has %d connected agents", m.deps.MaxSessionsPerToken)
	}
	byTok[s.id] = s
	m.sessions[s.id] = s
	m.mu.Unlock()
	if supersede != nil {
		supersede.log.Info("session superseded by new connection")
		supersede.Close(tunnel.ByeSuperseded, true)
	}
	return nil
}

func (m *Manager) unregister(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.sessions[s.id]; ok && cur == s {
		delete(m.sessions, s.id)
	}
	if byTok := m.byToken[s.tokenID]; byTok != nil {
		if cur, ok := byTok[s.id]; ok && cur == s {
			delete(byTok, s.id)
		}
		if len(byTok) == 0 {
			delete(m.byToken, s.tokenID)
		}
	}
}

// rejectIncomingStreams closes any data stream the agent tries to open; in
// protocol v1 only the relay opens data streams.
func (s *Session) rejectIncomingStreams() {
	for {
		st, err := s.mux.AcceptStream()
		if err != nil {
			return
		}
		_ = st.Close()
	}
}

func (s *Session) pingLoop() {
	t := time.NewTicker(tunnel.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, s.lastPong.Load())) > tunnel.PingDeadAfter {
				s.log.Warn("agent unresponsive, closing")
				s.Close(tunnel.ErrTimeout, false)
				return
			}
			if _, err := s.ctrl.Send(tunnel.TypePing, 0, &tunnel.Ping{TS: time.Now().UTC()}); err != nil {
				s.Close("write_failed", false)
				return
			}
		}
	}
}

func (s *Session) readLoop() {
	var window time.Time
	var count int
	for {
		env, err := s.ctrl.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && s.CloseReason() == "" {
				if errors.Is(err, tunnel.ErrFrameTooLarge) {
					_ = s.ctrl.SendError(0, tunnel.ErrCodeFrameTooLarge, "control frame exceeds 1 MiB", true)
				}
				s.log.Debug("control stream read ended", "err", err)
			}
			return
		}
		// Any message counts as liveness.
		s.lastPong.Store(time.Now().UnixNano())

		now := time.Now()
		if now.Sub(window) >= time.Second {
			window, count = now, 0
		}
		count++
		if count > MaxControlMessagesPerSec {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrRateLimited, "too many control messages", false)
			continue
		}
		if stop := s.handle(env); stop {
			return
		}
	}
}

// handle processes one control message; it returns true when the session
// should end.
func (s *Session) handle(env *tunnel.Envelope) bool {
	ctx := context.Background()
	switch env.T {
	case tunnel.TypePing:
		var p tunnel.Ping
		_ = env.Decode(&p)
		_, _ = s.ctrl.Send(tunnel.TypePong, env.ID, &tunnel.Pong{TS: p.TS})
	case tunnel.TypePong:
		var p tunnel.Pong
		if env.Decode(&p) == nil && !p.TS.IsZero() {
			s.rttNs.Store(int64(time.Since(p.TS)))
		}
	case tunnel.TypeRouteAnnounce:
		var ra tunnel.RouteAnnounce
		if err := env.Decode(&ra); err != nil {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrMalformed, err.Error(), false)
			return false
		}
		if len(ra.Routes) > MaxRoutesPerSession {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrInvalid, fmt.Sprintf("at most %d routes per session", MaxRoutesPerSession), false)
			return false
		}
		ack := s.m.deps.Registry.Announce(ctx, s, s.scope, ra.Routes)
		for _, rej := range ack.Rejected {
			s.log.Info("route rejected", "route", rej.RouteID, "code", rej.Code, "msg", rej.Message)
		}
		_, _ = s.ctrl.Send(tunnel.TypeRouteAck, env.ID, &ack)
	case tunnel.TypeRouteWithdraw:
		var rw tunnel.RouteWithdraw
		if err := env.Decode(&rw); err != nil {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrMalformed, err.Error(), false)
			return false
		}
		s.m.deps.Registry.Withdraw(ctx, s, rw.RouteIDs)
		_, _ = s.ctrl.Send(tunnel.TypeRouteAck, env.ID, &tunnel.RouteAck{Accepted: []tunnel.RouteAccepted{}, Rejected: []tunnel.RouteRejected{}})
	case tunnel.TypePolicySync:
		if s.m.deps.OnPolicySync == nil {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrUnknownType, "this relay does not enforce policies yet", false)
			return false
		}
		var ps tunnel.PolicySync
		if err := env.Decode(&ps); err != nil {
			_ = s.ctrl.SendError(env.ID, tunnel.ErrMalformed, err.Error(), false)
			return false
		}
		ack, err := s.m.deps.OnPolicySync(s, &ps)
		if err != nil {
			var ve *model.ValidationError
			if errors.As(err, &ve) {
				_ = s.ctrl.SendError(env.ID, tunnel.ErrInvalid, ve.Error(), false)
			} else {
				_ = s.ctrl.SendError(env.ID, tunnel.ErrInternal, err.Error(), false)
			}
			return false
		}
		_, _ = s.ctrl.Send(tunnel.TypePolicyAck, env.ID, &ack)
	case tunnel.TypeGoodbye:
		var g tunnel.Goodbye
		_ = env.Decode(&g)
		s.Close("agent_goodbye:"+g.Reason, false)
		return true
	case tunnel.TypeError:
		var e tunnel.Error
		_ = env.Decode(&e)
		s.log.Warn("agent reported error", "code", e.Code, "msg", e.Message, "fatal", e.Fatal)
		if e.Fatal {
			s.Close("agent_error:"+e.Code, false)
			return true
		}
	case tunnel.TypeHello:
		_ = s.ctrl.SendError(env.ID, tunnel.ErrInvalid, "Hello already received", false)
	default:
		_ = s.ctrl.SendError(env.ID, tunnel.ErrUnknownType, fmt.Sprintf("unsupported message type %q", env.T), false)
	}
	return false
}

// SendEvent queues an AccessEvent for delivery to the agent. It never blocks;
// if the buffer is full the event is dropped and counted.
func (s *Session) SendEvent(ev model.AccessEvent) {
	if s.events == nil {
		return
	}
	select {
	case s.events <- ev:
	default:
		s.evtDropped.Add(1)
	}
}

// eventLoop batches queued AccessEvents and sends them on the control stream.
func (s *Session) eventLoop() {
	t := time.NewTicker(eventFlushInterval)
	defer t.Stop()
	batch := make([]model.AccessEvent, 0, eventBatchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		dropped := int(s.evtDropped.Swap(0))
		_, _ = s.ctrl.Send(tunnel.TypeAccessEvent, 0, &tunnel.AccessEventBatch{Events: batch, Dropped: dropped})
		batch = batch[:0]
	}
	for {
		select {
		case <-s.done:
			return
		case ev := <-s.events:
			batch = append(batch, ev)
			if len(batch) >= eventBatchMax {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}
