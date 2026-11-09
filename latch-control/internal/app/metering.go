package app

import (
	"context"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/plan"
	"github.com/kayaramazan/latch/latch-control/internal/store"
	"github.com/kayaramazan/latch/proto/control"
)

// meter computes per-token usage deltas from cumulative relay counters and
// attributes them to accounts.
type meter struct {
	st   store.Store
	mu   sync.Mutex
	last map[string]control.Usage // tokenID -> last cumulative
}

func newMeter(st store.Store) *meter { return &meter{st: st, last: map[string]control.Usage{}} }

// IngestHeartbeat folds a relay heartbeat's per-token usage into account rollups.
func (s *Server) IngestHeartbeat(ctx context.Context, hb control.Heartbeat) {
	for tokenID, cur := range hb.Health.TokenUsage {
		s.m.mu.Lock()
		prev := s.m.last[tokenID]
		s.m.last[tokenID] = cur
		s.m.mu.Unlock()
		din, dout, dreq := delta(prev.BytesIn, cur.BytesIn), delta(prev.BytesOut, cur.BytesOut), delta(prev.Requests, cur.Requests)
		if din == 0 && dout == 0 && dreq == 0 {
			continue
		}
		acct, err := s.st.AccountForToken(ctx, tokenID)
		if err != nil {
			continue
		}
		_ = s.st.AddUsage(ctx, acct, time.Now().UTC(), din, dout, dreq)
	}
}

// delta returns cur-prev, or cur if the counter reset (relay restart).
func delta(prev, cur int64) int64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}

// quotaLoop periodically enforces bandwidth quotas: a soft warning at the
// threshold, and a hard cut (suspend) for hard-capped plans over 100%.
func (s *Server) quotaLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.enforceQuotas(ctx)
		}
	}
}

func (s *Server) enforceQuotas(ctx context.Context) {
	accts, err := s.st.ListAllAccounts(ctx)
	if err != nil {
		return
	}
	monthStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, a := range accts {
		if a.Status != store.AccountActive {
			continue
		}
		p := plan.Get(a.PlanID)
		bin, bout, _, err := s.st.MonthUsage(ctx, a.ID, monthStart)
		if err != nil {
			continue
		}
		used := bin + bout
		allowance := p.BandwidthBytes()
		if allowance <= 0 {
			continue
		}
		switch {
		case p.HardCapBandwidth && used >= allowance:
			s.suspend(ctx, a.ID, "bandwidth quota exceeded")
		case float64(used) >= plan.SoftThreshold*float64(allowance):
			s.warnOnce(ctx, a.ID, "bandwidth_soft_limit")
		}
	}
}

// warnOnce emits a soft-limit audit entry at most once per account per day.
func (s *Server) warnOnce(ctx context.Context, accountID, kind string) {
	key := accountID + ":" + kind + ":" + time.Now().UTC().Format("2006-01-02")
	s.m.mu.Lock()
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	if s.warned[key] {
		s.m.mu.Unlock()
		return
	}
	s.warned[key] = true
	s.m.mu.Unlock()
	_ = s.st.AppendAudit(ctx, store.AuditEntry{ID: id("aud"), AccountID: accountID, Actor: "system",
		Action: kind, TargetType: "account", TargetID: accountID, CreatedAt: time.Now().UTC()})
}
