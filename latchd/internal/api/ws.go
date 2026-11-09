package api

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/kayaramazan/latch/latchd/internal/tunnel"
)

// wsMessage is one frame pushed to a WebSocket client.
type wsMessage struct {
	Type    string      `json:"type"` // event | status | health | config | hello
	Data    interface{} `json:"data,omitempty"`
	Version int         `json:"version,omitempty"`
}

// serveWS streams live events, status/health changes and config-version bumps.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost*", "127.0.0.1*"},
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx := r.Context()
	_ = wsjson.Write(ctx, c, wsMessage{Type: "hello"})

	evCh, cancelEv := s.events.Subscribe("")
	defer cancelEv()
	cfgCh, cancelCfg := s.store.Subscribe()
	defer cancelCfg()

	// Detect client close.
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	var lastStatus string

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-evCh:
			if wsjson.Write(ctx, c, wsMessage{Type: "event", Data: ev}) != nil {
				return
			}
		case ch, ok := <-cfgCh:
			if !ok {
				return
			}
			if wsjson.Write(ctx, c, wsMessage{Type: "config", Version: ch.Version}) != nil {
				return
			}
		case <-ticker.C:
			status := s.tun.Status()
			// Only push status when it changed, to keep the stream quiet.
			key := statusKey(status)
			if key != lastStatus {
				lastStatus = key
				if wsjson.Write(ctx, c, wsMessage{Type: "status", Data: status}) != nil {
					return
				}
			}
			if s.health != nil {
				if wsjson.Write(ctx, c, wsMessage{Type: "health", Data: s.health()}) != nil {
					return
				}
			}
		}
	}
}

func statusKey(status []tunnel.ClientStatus) string {
	var b []byte
	for _, st := range status {
		b = append(b, st.RelayID...)
		b = append(b, '|')
		b = append(b, string(st.State)...)
		b = append(b, '|')
		b = append(b, st.SyncState...)
		b = append(b, ';')
	}
	return string(b)
}
