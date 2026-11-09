package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/kayaramazan/latch/proto/tunnel"
)

// conn is one live tunnel connection to a relay. It is created per connect
// attempt and discarded when the connection ends.
type conn struct {
	c     *Client
	mux   *yamux.Session
	tconn *tls.Conn
	ctrl  *tunnel.Codec
	ack   tunnel.HelloAck

	lastPong  atomic.Int64 // unix nanos
	closeOnce sync.Once
	done      chan struct{}
	reason    atomic.Value // string
}

// handshake sends Hello and waits for HelloAck. fatal is true when the relay
// refused the connection (auth, version), so the caller can log accordingly.
func (cn *conn) handshake() (fatal bool, err error) {
	hello := &tunnel.Hello{
		ProtoMin: tunnel.MinProtocolVersion, ProtoMax: tunnel.ProtocolVersion,
		AgentVersion: cn.c.agentVersion, AgentID: cn.c.agentID, Token: cn.c.relay.Token,
		Capabilities: []string{tunnel.CapTCP, tunnel.CapEvents},
	}
	id, err := cn.ctrl.Send(tunnel.TypeHello, 0, hello)
	if err != nil {
		return false, fmt.Errorf("send hello: %w", err)
	}
	_ = cn.tconn.SetReadDeadline(time.Now().Add(tunnel.HelloTimeout))
	env, err := cn.ctrl.Recv()
	if err != nil {
		return false, fmt.Errorf("read hello ack: %w", err)
	}
	_ = cn.tconn.SetReadDeadline(time.Time{})

	switch env.T {
	case tunnel.TypeHelloAck:
		if err := env.Decode(&cn.ack); err != nil {
			return false, fmt.Errorf("decode hello ack: %w", err)
		}
		_ = id
		cn.lastPong.Store(time.Now().UnixNano())
		return false, nil
	case tunnel.TypeError:
		var e tunnel.Error
		_ = env.Decode(&e)
		if e.Code == tunnel.ErrVersion {
			return true, fmt.Errorf("protocol mismatch: relay supports %d-%d, this agent supports %d-%d (update the %s side)",
				e.RelayMin, e.RelayMax, tunnel.MinProtocolVersion, tunnel.ProtocolVersion, olderSide(e))
		}
		return true, fmt.Errorf("relay refused connection: %s", e.Error())
	default:
		return false, fmt.Errorf("expected HelloAck, got %s", env.T)
	}
}

func olderSide(e tunnel.Error) string {
	if e.RelayMax < tunnel.ProtocolVersion {
		return "relay"
	}
	return "agent"
}

// announce sends the full set of routes. The relay withdraws any route not in
// the set. Errors are logged; the control loop will notice a dead connection.
func (cn *conn) announce(specs []tunnel.RouteSpec) {
	if specs == nil {
		specs = []tunnel.RouteSpec{}
	}
	if _, err := cn.ctrl.Send(tunnel.TypeRouteAnnounce, 0, &tunnel.RouteAnnounce{Routes: specs}); err != nil {
		cn.c.log.Debug("route announce failed", "err", err)
	}
}

func (cn *conn) closeReason() string {
	v, _ := cn.reason.Load().(string)
	return v
}

func (cn *conn) close(reason string) {
	cn.closeOnce.Do(func() {
		cn.reason.Store(reason)
		_ = cn.mux.Close()
		_ = cn.tconn.Close()
		close(cn.done)
	})
}

// serve runs the control, ping and data-stream loops until the connection
// ends, then returns. ctx cancellation triggers a graceful Goodbye.
func (cn *conn) serve(ctx context.Context) {
	go cn.acceptStreams()
	go cn.pingLoop()
	go func() {
		select {
		case <-ctx.Done():
			_, _ = cn.ctrl.Send(tunnel.TypeGoodbye, 0, &tunnel.Goodbye{Reason: tunnel.ByeShutdown})
			cn.close("agent_shutdown")
		case <-cn.done:
		}
	}()
	cn.readLoop()
	cn.close("connection_lost")
	<-cn.done
}

func (cn *conn) readLoop() {
	for {
		env, err := cn.ctrl.Recv()
		if err != nil {
			if cn.closeReason() == "" && !errors.Is(err, io.EOF) {
				cn.c.log.Debug("control stream ended", "err", err)
			}
			return
		}
		cn.lastPong.Store(time.Now().UnixNano())
		switch env.T {
		case tunnel.TypePing:
			var p tunnel.Ping
			_ = env.Decode(&p)
			_, _ = cn.ctrl.Send(tunnel.TypePong, env.ID, &tunnel.Pong{TS: p.TS})
		case tunnel.TypePong:
			var p tunnel.Pong
			if env.Decode(&p) == nil && !p.TS.IsZero() {
				cn.c.rttNs.Store(int64(time.Since(p.TS)))
			}
		case tunnel.TypeRouteAck:
			var ack tunnel.RouteAck
			if env.Decode(&ack) == nil {
				cn.applyRouteAck(&ack)
			}
		case tunnel.TypeGoodbye:
			var g tunnel.Goodbye
			_ = env.Decode(&g)
			cn.close("relay_goodbye:" + g.Reason)
			return
		case tunnel.TypeError:
			var e tunnel.Error
			_ = env.Decode(&e)
			cn.c.log.Warn("relay reported error", "code", e.Code, "msg", e.Message, "fatal", e.Fatal)
			if e.Fatal {
				cn.close("relay_error:" + e.Code)
				return
			}
		case tunnel.TypePolicyAck, tunnel.TypeAccessEvent:
			// Phase 5/7; ignored for now.
		default:
			cn.c.log.Debug("unexpected control message", "type", env.T)
		}
	}
}

func (cn *conn) applyRouteAck(ack *tunnel.RouteAck) {
	accepted := make([]string, 0, len(ack.Accepted))
	urls := make([]string, 0)
	for _, a := range ack.Accepted {
		if a.TCPPort != 0 {
			accepted = append(accepted, fmt.Sprintf("tcp/%d", a.TCPPort))
		}
		for _, h := range a.Hostnames {
			accepted = append(accepted, h)
		}
		urls = append(urls, a.PublicURLs...)
	}
	rejected := make([]string, 0, len(ack.Rejected))
	for _, r := range ack.Rejected {
		rejected = append(rejected, fmt.Sprintf("%s: %s (%s)", r.RouteID, r.Message, r.Code))
		cn.c.log.Warn("route rejected by relay", "route", r.RouteID, "code", r.Code, "msg", r.Message)
	}
	cn.c.accepted.Store(accepted)
	cn.c.rejected.Store(rejected)
	cn.c.publicURLs.Store(urls)
}

func (cn *conn) pingLoop() {
	t := time.NewTicker(tunnel.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-cn.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, cn.lastPong.Load())) > tunnel.PingDeadAfter {
				cn.c.log.Warn("relay unresponsive, closing")
				cn.close("relay_timeout")
				return
			}
			if _, err := cn.ctrl.Send(tunnel.TypePing, 0, &tunnel.Ping{TS: time.Now().UTC()}); err != nil {
				cn.close("write_failed")
				return
			}
		}
	}
}

// acceptStreams handles data streams the relay opens: read the header, dial
// the local target, report status, then copy bytes both ways.
func (cn *conn) acceptStreams() {
	for {
		st, err := cn.mux.AcceptStream()
		if err != nil {
			return
		}
		go cn.handleStream(st)
	}
}

func (cn *conn) handleStream(st *yamux.Stream) {
	_ = st.SetDeadline(time.Now().Add(localDialWait))
	hdr, err := tunnel.ReadStreamHeader(st)
	if err != nil {
		_ = st.Close()
		return
	}
	target := cn.c.target(hdr.RouteID)
	if target == "" {
		_ = tunnel.WriteStatus(st, tunnel.StatusNoRoute)
		_ = st.Close()
		return
	}
	local, err := net.DialTimeout("tcp", target, localDialWait)
	if err != nil {
		cn.c.log.Debug("local dial failed", "target", target, "err", err)
		_ = tunnel.WriteStatus(st, tunnel.StatusDialFailed)
		_ = st.Close()
		return
	}
	if err := tunnel.WriteStatus(st, tunnel.StatusOK); err != nil {
		_ = local.Close()
		_ = st.Close()
		return
	}
	_ = st.SetDeadline(time.Time{})
	cn.c.streams.Add(1)
	pipe(st, local, &cn.c.bytesIn, &cn.c.bytesOut)
}
