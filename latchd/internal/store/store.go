// Package store is latchd's SQLite-backed configuration store. Every mutation
// writes a new, fully validated snapshot as an immutable config version, so
// any version can be inspected, diffed and rolled back.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/kayaramazan/latch/proto/model"
)

// Options tune the store.
type Options struct {
	// MaxVersions is how many config versions to keep; older ones are pruned.
	// Zero means DefaultMaxVersions.
	MaxVersions int
}

// DefaultMaxVersions is the default retention for config versions.
const DefaultMaxVersions = 200

// ErrVersionNotFound is returned for an unknown config version.
var ErrVersionNotFound = errors.New("store: config version not found")

// ErrNoChanges is returned by Update when the mutation produced an identical
// snapshot; no version is written.
var ErrNoChanges = errors.New("store: no changes")

// VersionInfo describes one stored config version.
type VersionInfo struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	Author    string    `json:"author"`
	Summary   string    `json:"summary"`
}

// Result is what a successful mutation returns.
type Result struct {
	Version  int             `json:"version"`
	Diff     model.Diff      `json:"diff"`
	Snapshot *model.Snapshot `json:"-"`
}

// Change is delivered to subscribers after every new version.
type Change struct {
	Version  int
	Diff     model.Diff
	Snapshot *model.Snapshot
}

// Store is safe for concurrent use. Writes are serialized.
type Store struct {
	db   *sql.DB
	opts Options

	mu      sync.Mutex // serializes mutations
	current *model.Snapshot
	version int

	subMu sync.Mutex
	subs  map[int]chan Change
	subID int
}

// Open opens (or creates) the database at path, runs migrations and loads the
// current snapshot. A fresh database gets version 1 = defaults. The file is
// created with mode 0600.
func Open(path string, opts Options) (*Store, error) {
	if opts.MaxVersions <= 0 {
		opts.MaxVersions = DefaultMaxVersions
	}
	if err := touch0600(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps WAL simple and avoids SQLITE_BUSY in tests
	s := &Store{db: db, opts: opts, subs: map[int]chan Change{}}
	ctx := context.Background()
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.load(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func touch0600(path string) error {
	if path == ":memory:" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("store: create %s: %w", path, err)
	}
	_ = f.Close()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("store: chmod %s: %w", path, err)
		}
	}
	return nil
}

// Close closes the database and all subscriptions.
func (s *Store) Close() error {
	s.subMu.Lock()
	for id, ch := range s.subs {
		close(ch)
		delete(s.subs, id)
	}
	s.subMu.Unlock()
	return s.db.Close()
}

func (s *Store) load(ctx context.Context) error {
	row := s.db.QueryRowContext(ctx, `SELECT version, snapshot FROM config_versions ORDER BY version DESC LIMIT 1`)
	var version int
	var raw []byte
	err := row.Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		snap := model.NewSnapshot()
		if err := model.Validate(snap); err != nil {
			return fmt.Errorf("store: default snapshot invalid: %w", err)
		}
		v, err := s.insertVersion(ctx, "system", "initial configuration", snap)
		if err != nil {
			return err
		}
		s.current, s.version = snap, v
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: load current version: %w", err)
	}
	snap, err := decode(raw)
	if err != nil {
		return fmt.Errorf("store: version %d: %w", version, err)
	}
	s.current, s.version = snap, version
	return nil
}

func decode(raw []byte) (*model.Snapshot, error) {
	var snap model.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	snap.Normalize()
	return &snap, nil
}

func cloneSnapshot(s *model.Snapshot) (*model.Snapshot, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return decode(b)
}

// Current returns a copy of the current snapshot and its version.
func (s *Store) Current() (*model.Snapshot, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := cloneSnapshot(s.current)
	if err != nil {
		panic("store: current snapshot does not round-trip: " + err.Error())
	}
	return c, s.version
}

// Update applies fn to a copy of the current snapshot, normalizes and
// validates the result, and writes it as a new version. fn may return an
// error to abort. Validation failures are returned as *model.ValidationError.
func (s *Store) Update(ctx context.Context, author, summary string, fn func(snap *model.Snapshot) error) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next, err := cloneSnapshot(s.current)
	if err != nil {
		return Result{}, err
	}
	if err := fn(next); err != nil {
		return Result{}, err
	}
	return s.commitLocked(ctx, author, summary, next)
}

// commitLocked validates next, diffs it against current, persists and
// notifies. Caller holds s.mu.
func (s *Store) commitLocked(ctx context.Context, author, summary string, next *model.Snapshot) (Result, error) {
	next.Normalize()
	if err := model.Validate(next); err != nil {
		return Result{}, err
	}
	diff := model.ComputeDiff(s.current, next)
	if diff.Empty() {
		return Result{Version: s.version, Diff: diff, Snapshot: next}, ErrNoChanges
	}
	stampRoutes(s.current, next, s.version+1)
	if summary == "" {
		summary = diff.Summary()
	}
	v, err := s.insertVersion(ctx, author, summary, next)
	if err != nil {
		return Result{}, err
	}
	s.current, s.version = next, v
	s.notify(Change{Version: v, Diff: diff, Snapshot: next})
	return Result{Version: v, Diff: diff, Snapshot: next}, nil
}

// stampRoutes sets UpdatedAt/Version on routes that actually changed and
// CreatedAt on new ones.
func stampRoutes(old, next *model.Snapshot, version int) {
	now := time.Now().UTC().Truncate(time.Second)
	for i := range next.Routes {
		r := &next.Routes[i]
		prev := old.Route(r.ID)
		switch {
		case prev == nil:
			if r.CreatedAt.IsZero() {
				r.CreatedAt = now
			}
			r.UpdatedAt, r.Version = now, version
		default:
			r.CreatedAt = prev.CreatedAt
			if !routeSame(*prev, *r) {
				r.UpdatedAt, r.Version = now, version
			} else {
				r.UpdatedAt, r.Version = prev.UpdatedAt, prev.Version
			}
		}
	}
}

func routeSame(a, b model.Route) bool {
	d := model.ComputeDiff(&model.Snapshot{Routes: []model.Route{a}}, &model.Snapshot{Routes: []model.Route{b}})
	return d.Empty()
}

func (s *Store) insertVersion(ctx context.Context, author, summary string, snap *model.Snapshot) (int, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return 0, fmt.Errorf("store: encode snapshot: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM config_versions`).Scan(&next); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO config_versions (version, created_at, author, summary, snapshot) VALUES (?, ?, ?, ?, ?)`,
		next, time.Now().UTC().Format(time.RFC3339Nano), author, summary, raw)
	if err != nil {
		return 0, fmt.Errorf("store: insert version: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`DELETE FROM config_versions WHERE version <= ? - ?`, next, s.opts.MaxVersions)
	if err != nil {
		return 0, fmt.Errorf("store: prune versions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// Versions lists stored versions, newest first. before=0 starts from the
// newest; limit<=0 means 50.
func (s *Store) Versions(ctx context.Context, limit, before int) ([]VersionInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	if before <= 0 {
		before = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT version, created_at, author, summary FROM config_versions WHERE version < ? ORDER BY version DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionInfo
	for rows.Next() {
		var v VersionInfo
		var ts string
		if err := rows.Scan(&v.Version, &ts, &v.Author, &v.Summary); err != nil {
			return nil, err
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, v)
	}
	if out == nil {
		out = []VersionInfo{}
	}
	return out, rows.Err()
}

// Version returns one stored snapshot.
func (s *Store) Version(ctx context.Context, n int) (*model.Snapshot, VersionInfo, error) {
	var info VersionInfo
	var ts string
	var raw []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT version, created_at, author, summary, snapshot FROM config_versions WHERE version = ?`, n).
		Scan(&info.Version, &ts, &info.Author, &info.Summary, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, VersionInfo{}, ErrVersionNotFound
	}
	if err != nil {
		return nil, VersionInfo{}, err
	}
	info.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	snap, err := decode(raw)
	if err != nil {
		return nil, VersionInfo{}, fmt.Errorf("store: version %d: %w", n, err)
	}
	return snap, info, nil
}

// Diff compares two stored versions (0 = current).
func (s *Store) Diff(ctx context.Context, a, b int) (model.Diff, error) {
	sa, err := s.snapshotOrCurrent(ctx, a)
	if err != nil {
		return model.Diff{}, err
	}
	sb, err := s.snapshotOrCurrent(ctx, b)
	if err != nil {
		return model.Diff{}, err
	}
	return model.ComputeDiff(sa, sb), nil
}

func (s *Store) snapshotOrCurrent(ctx context.Context, n int) (*model.Snapshot, error) {
	if n == 0 {
		snap, _ := s.Current()
		return snap, nil
	}
	snap, _, err := s.Version(ctx, n)
	return snap, err
}

// Rollback writes version n's snapshot as a new version.
func (s *Store) Rollback(ctx context.Context, n int, author string) (Result, error) {
	snap, _, err := s.Version(ctx, n)
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitLocked(ctx, author, fmt.Sprintf("rollback to version %d", n), snap)
}

// Export returns the current snapshot for backup, including secrets.
func (s *Store) Export() (*model.Snapshot, int) {
	return s.Current()
}

// ImportMode selects how Import treats existing configuration.
type ImportMode string

const (
	// ImportReplace discards the current configuration.
	ImportReplace ImportMode = "replace"
	// ImportMerge upserts objects by ID and keeps everything else; settings
	// are taken from the import.
	ImportMerge ImportMode = "merge"
)

// Import validates and writes a snapshot from a backup.
func (s *Store) Import(ctx context.Context, snap *model.Snapshot, mode ImportMode, author string) (Result, error) {
	in, err := cloneSnapshot(snap)
	if err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var next *model.Snapshot
	switch mode {
	case ImportReplace:
		next = in
	case ImportMerge:
		next, err = cloneSnapshot(s.current)
		if err != nil {
			return Result{}, err
		}
		mergeInto(next, in)
	default:
		return Result{}, fmt.Errorf("store: unknown import mode %q", mode)
	}
	return s.commitLocked(ctx, author, "import ("+string(mode)+")", next)
}

func mergeInto(dst, src *model.Snapshot) {
	for _, r := range src.Routes {
		if cur := dst.Route(r.ID); cur != nil {
			*cur = r
		} else {
			dst.Routes = append(dst.Routes, r)
		}
	}
	for _, p := range src.IPPolicies {
		if cur := dst.IPPolicy(p.ID); cur != nil {
			*cur = p
		} else {
			dst.IPPolicies = append(dst.IPPolicies, p)
		}
	}
	for _, r := range src.RateLimits {
		if cur := dst.RateLimit(r.ID); cur != nil {
			*cur = r
		} else {
			dst.RateLimits = append(dst.RateLimits, r)
		}
	}
	for _, r := range src.Relays {
		if cur := dst.Relay(r.ID); cur != nil {
			*cur = r
		} else {
			dst.Relays = append(dst.Relays, r)
		}
	}
	dst.Settings = src.Settings
}

// Subscribe returns a channel that receives every new version. The channel
// is buffered; a slow subscriber that falls 64 changes behind is dropped
// (its channel closed) rather than blocking writers. cancel removes it.
func (s *Store) Subscribe() (<-chan Change, func()) {
	ch := make(chan Change, 64)
	s.subMu.Lock()
	s.subID++
	id := s.subID
	s.subs[id] = ch
	s.subMu.Unlock()
	cancel := func() {
		s.subMu.Lock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
		s.subMu.Unlock()
	}
	return ch, cancel
}

func (s *Store) notify(c Change) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for id, ch := range s.subs {
		select {
		case ch <- c:
		default:
			delete(s.subs, id)
			close(ch)
		}
	}
}
