// Package usage tracks the relay's cumulative traffic counters so the control
// plane can meter accounts. Counters are monotonic since process start; the
// control plane computes deltas.
package usage

import (
	"sync/atomic"

	"github.com/kayaramazan/latch/proto/control"
	"github.com/kayaramazan/latch/proto/model"
)

// Tracker accumulates usage from AccessEvents. Safe for concurrent use.
type Tracker struct {
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	requests atomic.Int64
	denials  atomic.Int64
}

// New returns a Tracker.
func New() *Tracker { return &Tracker{} }

// Record folds one AccessEvent into the counters.
func (t *Tracker) Record(ev model.AccessEvent) {
	t.requests.Add(1)
	if ev.BytesIn > 0 {
		t.bytesIn.Add(ev.BytesIn)
	}
	if ev.BytesOut > 0 {
		t.bytesOut.Add(ev.BytesOut)
	}
	if ev.Decision == model.DecisionDeny {
		t.denials.Add(1)
	}
}

// Snapshot returns the current counters.
func (t *Tracker) Snapshot() control.Usage {
	reqs := t.requests.Load()
	return control.Usage{
		BytesIn:     t.bytesIn.Load(),
		BytesOut:    t.bytesOut.Load(),
		Requests:    reqs,
		Denials:     t.denials.Load(),
		Connections: reqs, // one tunnel stream per request in v1
	}
}
