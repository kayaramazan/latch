package events

import (
	"context"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

const retentionInterval = time.Minute

// retentionLoop enforces the age and size limits periodically.
func (s *Store) retentionLoop(ctx context.Context) {
	defer s.wg.Done()
	// Run once shortly after startup, then on an interval.
	first := time.NewTimer(5 * time.Second)
	defer first.Stop()
	t := time.NewTicker(retentionInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.enforce()
		case <-t.C:
			s.enforce()
		}
	}
}

// enforce deletes events older than the retention window and trims by size.
// Any database error is logged, not fatal.
func (s *Store) enforce() {
	cutoff := time.Now().Add(-time.Duration(s.retentionDays) * 24 * time.Hour).Unix()
	if _, err := s.db.Exec(`DELETE FROM events WHERE ts_unix < ?`, cutoff); err != nil {
		s.log.Warn("events: age retention failed", "err", err)
	}
	s.trimBySize()
}

// trimBySize deletes the oldest events while the database exceeds the size cap.
// On disk-full or other errors it records a warning event and returns.
func (s *Store) trimBySize() {
	maxBytes := int64(s.maxSizeMB) * 1024 * 1024
	for i := 0; i < 64; i++ { // bounded passes
		size, err := s.dbSizeBytes()
		if err != nil {
			s.log.Warn("events: size check failed", "err", err)
			return
		}
		if size <= maxBytes {
			return
		}
		// Delete the oldest ~5% of rows (at least 1000).
		var total int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&total); err != nil {
			return
		}
		del := total / 20
		if del < 1000 {
			del = 1000
		}
		res, err := s.db.Exec(`DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY id ASC LIMIT ?)`, del)
		if err != nil {
			s.warnEvent("event store is over its size limit and could not be trimmed: " + err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return
		}
		_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		s.log.Info("events: trimmed oldest events to stay under size cap", "deleted", del)
	}
}

// dbSizeBytes returns the logical database size (page_count * page_size).
func (s *Store) dbSizeBytes() (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

// warnEvent records a synthetic warning into the ring (not persisted) so the
// operator sees storage trouble without the daemon crashing.
func (s *Store) warnEvent(msg string) {
	s.appendRing(model.AccessEvent{
		TS: time.Now().UTC(), Source: model.SourceLocal, Where: "agent",
		Decision: model.DecisionDeny, DenyReason: "event_store", Path: msg,
	})
	s.log.Warn("events: " + msg)
}
