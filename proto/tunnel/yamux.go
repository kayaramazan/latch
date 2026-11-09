package tunnel

import (
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

// YamuxConfig returns the session configuration both sides use. Latch runs
// its own Ping/Pong on the control stream, so yamux keep-alives are off.
func YamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = false
	cfg.MaxStreamWindowSize = 1 << 20
	// Disable yamux's session-level open timeout: it closes the WHOLE session
	// when any stream stays pending. Latch bounds each OpenStream with its own
	// deadline and runs Ping/Pong liveness on the control stream instead.
	cfg.StreamOpenTimeout = 0
	cfg.StreamCloseTimeout = 30 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}
