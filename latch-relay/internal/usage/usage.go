// Package usage tracks the relay's traffic counters, globally and per token, so
// the control plane can meter accounts. Counters are monotonic since process
// start; the control plane computes deltas.
package usage

import (
	"sync"
	"sync/atomic"

	"github.com/kayaramazan/latch/proto/control"
	"github.com/kayaramazan/latch/proto/model"
)

// Tracker accumulates usage from AccessEvents. Safe for concurrent use.
type Tracker struct {
	g counters

	mu      sync.Mutex
	byToken map[string]*counters
}

type counters struct {
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	requests atomic.Int64
	denials  atomic.Int64
}

func (c *counters) record(ev model.AccessEvent) {
	c.requests.Add(1)
	if ev.BytesIn > 0 {
		c.bytesIn.Add(ev.BytesIn)
	}
	if ev.BytesOut > 0 {
		c.bytesOut.Add(ev.BytesOut)
	}
	if ev.Decision == model.DecisionDeny {
		c.denials.Add(1)
	}
}

func (c *counters) snapshot() control.Usage {
	reqs := c.requests.Load()
	return control.Usage{BytesIn: c.bytesIn.Load(), BytesOut: c.bytesOut.Load(),
		Requests: reqs, Denials: c.denials.Load(), Connections: reqs}
}

// New returns a Tracker.
func New() *Tracker { return &Tracker{byToken: map[string]*counters{}} }

// Record folds one event into the global counters.
func (t *Tracker) Record(ev model.AccessEvent) { t.g.record(ev) }

// RecordToken folds one event into the global and the per-token counters.
func (t *Tracker) RecordToken(tokenID string, ev model.AccessEvent) {
	t.g.record(ev)
	if tokenID == "" {
		return
	}
	t.mu.Lock()
	c := t.byToken[tokenID]
	if c == nil {
		c = &counters{}
		t.byToken[tokenID] = c
	}
	t.mu.Unlock()
	c.record(ev)
}

// Snapshot returns the global counters.
func (t *Tracker) Snapshot() control.Usage { return t.g.snapshot() }

// ByToken returns per-token usage.
func (t *Tracker) ByToken() map[string]control.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]control.Usage, len(t.byToken))
	for id, c := range t.byToken {
		out[id] = c.snapshot()
	}
	return out
}
