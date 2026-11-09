// Package events is latchd's unified observability store: every AccessEvent
// (local or relay) lands here. It keeps a fast in-memory ring for live
// following, persists to SQLite for query and dashboards, and enforces a
// retention policy by age and size. It never crashes the daemon on a write or
// disk error — it drops events and records a warning instead.
package events

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kayaramazan/latch/proto/model"
)

// Defaults (ROADMAP decision point: 7 days / 500 MB).
const (
	DefaultRetentionDays = 7
	DefaultMaxSizeMB     = 500
	DefaultRingSize      = 2048
	writeBatchMax        = 256
	writeFlushInterval   = 500 * time.Millisecond
	queueSize            = 4096
)

// Options configure a Store.
type Options struct {
	RetentionDays int
	MaxSizeMB     int
	RingSize      int
}

// Store is the event hub. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	log Logger

	retentionDays int
	maxSizeMB     int

	queue   chan model.AccessEvent
	dropped atomic.Int64

	// ring + subscriptions
	mu     sync.Mutex
	ring   []model.AccessEvent
	size   int
	next   int
	count  int
	subs   map[int]subscription
	subID  int
	closed bool

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

type subscription struct {
	ch      chan model.AccessEvent
	routeID string // "" = all
}

// Logger is the minimal logging surface the store needs.
type Logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warn(string, ...any) {}
func (nopLogger) Info(string, ...any) {}

// Open opens (or creates) the event database at path.
func Open(path string, opts Options, log Logger) (*Store, error) {
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = DefaultRetentionDays
	}
	if opts.MaxSizeMB <= 0 {
		opts.MaxSizeMB = DefaultMaxSizeMB
	}
	if opts.RingSize <= 0 {
		opts.RingSize = DefaultRingSize
	}
	if log == nil {
		log = nopLogger{}
	}
	if f, err := os.OpenFile(path, os.O_CREATE, 0o600); err == nil {
		_ = f.Close()
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("events: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{
		db: db, log: log, retentionDays: opts.RetentionDays, maxSizeMB: opts.MaxSizeMB,
		queue: make(chan model.AccessEvent, queueSize),
		ring:  make([]model.AccessEvent, opts.RingSize), size: opts.RingSize,
		subs: map[int]subscription{}, cancel: cancel,
	}
	s.wg.Add(2)
	go s.writeLoop(ctx)
	go s.retentionLoop(ctx)
	return s, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			ts          TEXT NOT NULL,
			ts_unix     INTEGER NOT NULL,
			source      TEXT, relay_id TEXT, route_id TEXT, hostname TEXT,
			client_ip   TEXT, country TEXT, method TEXT, path TEXT,
			status      INTEGER, duration_ms INTEGER, bytes_in INTEGER, bytes_out INTEGER,
			target      TEXT, decision TEXT, deny_reason TEXT, rule_id TEXT, where_loc TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts_unix);
		CREATE INDEX IF NOT EXISTS idx_events_route ON events(route_id);
		CREATE INDEX IF NOT EXISTS idx_events_decision ON events(decision, ts_unix);`)
	return err
}

// Append records an event: into the ring and live subscriptions immediately,
// and queued for the SQLite writer. Never blocks; a full queue drops and counts.
func (s *Store) Append(ev model.AccessEvent) {
	s.appendRing(ev)
	select {
	case s.queue <- ev:
	default:
		s.dropped.Add(1)
	}
}

func (s *Store) appendRing(ev model.AccessEvent) {
	s.mu.Lock()
	s.ring[s.next] = ev
	s.next = (s.next + 1) % s.size
	if s.count < s.size {
		s.count++
	}
	subs := make([]subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		if sub.routeID != "" && sub.routeID != ev.RouteID {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// Recent returns up to n most recent events from the ring, oldest first.
func (s *Store) Recent(n int) []model.AccessEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n > s.count {
		n = s.count
	}
	out := make([]model.AccessEvent, 0, n)
	start := (s.next - s.count + s.size) % s.size
	for i := s.count - n; i < s.count; i++ {
		out = append(out, s.ring[(start+i)%s.size])
	}
	return out
}

// Subscribe returns a channel of future events (optionally filtered by route)
// and a cancel function.
func (s *Store) Subscribe(routeID string) (<-chan model.AccessEvent, func()) {
	ch := make(chan model.AccessEvent, 256)
	s.mu.Lock()
	s.subID++
	id := s.subID
	s.subs[id] = subscription{ch: ch, routeID: routeID}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if sub, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(sub.ch)
		}
		s.mu.Unlock()
	}
}

func (s *Store) writeLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(writeFlushInterval)
	defer t.Stop()
	batch := make([]model.AccessEvent, 0, writeBatchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.insert(batch); err != nil {
			s.log.Warn("events: write failed, dropping batch", "err", err, "count", len(batch))
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// Drain quickly on shutdown.
			for {
				select {
				case ev := <-s.queue:
					batch = append(batch, ev)
					if len(batch) >= writeBatchMax {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case ev := <-s.queue:
			batch = append(batch, ev)
			if len(batch) >= writeBatchMax {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

func (s *Store) insert(batch []model.AccessEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO events
		(ts, ts_unix, source, relay_id, route_id, hostname, client_ip, country, method, path,
		 status, duration_ms, bytes_in, bytes_out, target, decision, deny_reason, rule_id, where_loc)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, ev := range batch {
		ts := ev.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		if _, err := stmt.Exec(ts.UTC().Format(time.RFC3339Nano), ts.Unix(),
			ev.Source, ev.RelayID, ev.RouteID, ev.Hostname, ev.ClientIP, ev.Country, ev.Method, ev.Path,
			ev.Status, ev.DurationMs, ev.BytesIn, ev.BytesOut, ev.Target, ev.Decision, ev.DenyReason, ev.RuleID, ev.Where); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Close stops the background loops and closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return s.db.Close()
}
