// Package store is the relay's SQLite database: agent tokens, announced
// routes (kept while agents are offline) and relay settings.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/kayaramazan/latch/proto/tunnel"
)

// ErrNotFound is returned for unknown rows.
var ErrNotFound = errors.New("relay store: not found")

// Token is a stored agent token. Hash is the argon2id encoding; the plain
// secret is never stored.
type Token struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Hash       string       `json:"-"`
	Scope      tunnel.Scope `json:"scope"`
	CreatedAt  time.Time    `json:"createdAt"`
	ExpiresAt  *time.Time   `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time   `json:"revokedAt,omitempty"`
	LastSeenAt *time.Time   `json:"lastSeenAt,omitempty"`
}

// Active reports whether the token may be used now.
func (t *Token) Active(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	if t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
		return false
	}
	return true
}

// HTTPRoute is an announced hostname+prefix, owned by a token.
type HTTPRoute struct {
	Hostname     string
	PathPrefix   string
	TokenID      string
	AgentID      string
	RouteID      string
	AnnouncedAt  time.Time
	LastOnlineAt time.Time
}

// TCPRoute is an announced TCP port, owned by a token.
type TCPRoute struct {
	Port         int
	TokenID      string
	AgentID      string
	RouteID      string
	AnnouncedAt  time.Time
	LastOnlineAt time.Time
}

// Store wraps the database. Safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database (mode 0600) and migrates it.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("relay store: create %s: %w", path, err)
		}
		_ = f.Close()
		if runtime.GOOS != "windows" {
			_ = os.Chmod(path, 0o600)
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE tokens (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		hash         TEXT NOT NULL,
		scope        TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		expires_at   TEXT,
		revoked_at   TEXT,
		last_seen_at TEXT
	);
	CREATE TABLE http_routes (
		hostname       TEXT NOT NULL,
		path_prefix    TEXT NOT NULL,
		token_id       TEXT NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
		agent_id       TEXT NOT NULL,
		route_id       TEXT NOT NULL,
		announced_at   TEXT NOT NULL,
		last_online_at TEXT NOT NULL,
		PRIMARY KEY (hostname, path_prefix)
	);
	CREATE TABLE tcp_routes (
		port           INTEGER PRIMARY KEY,
		token_id       TEXT NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
		agent_id       TEXT NOT NULL,
		route_id       TEXT NOT NULL,
		announced_at   TEXT NOT NULL,
		last_online_at TEXT NOT NULL
	);
	CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`,
	`CREATE TABLE custom_domains (
		domain      TEXT PRIMARY KEY,
		target      TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		verified_at TEXT,
		note        TEXT NOT NULL DEFAULT ''
	);`,
	`CREATE TABLE policies (
		token_id   TEXT PRIMARY KEY REFERENCES tokens(id) ON DELETE CASCADE,
		version    INTEGER NOT NULL,
		snapshot   TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("relay store: schema version %d is newer than this latch-relay supports (%d); update latch-relay", current, len(migrations))
	}
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("relay store: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, i+1, now()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// --- tokens ---

// InsertToken stores a new token.
func (s *Store) InsertToken(ctx context.Context, t *Token) error {
	scope, err := json.Marshal(t.Scope)
	if err != nil {
		return err
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, name, hash, scope, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Hash, string(scope), t.CreatedAt.Format(time.RFC3339Nano), fmtTimePtr(t.ExpiresAt))
	return err
}

const tokenCols = `id, name, hash, scope, created_at, expires_at, revoked_at, last_seen_at`

func scanToken(row interface{ Scan(...any) error }) (*Token, error) {
	var t Token
	var scope, created string
	var expires, revoked, seen sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &t.Hash, &scope, &created, &expires, &revoked, &seen); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scope), &t.Scope); err != nil {
		return nil, fmt.Errorf("relay store: token %s scope: %w", t.ID, err)
	}
	t.CreatedAt = parseTime(created)
	t.ExpiresAt, t.RevokedAt, t.LastSeenAt = parseTimePtr(expires), parseTimePtr(revoked), parseTimePtr(seen)
	return &t, nil
}

// Token returns one token by ID.
func (s *Store) Token(ctx context.Context, id string) (*Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// Tokens lists all tokens, newest first.
func (s *Store) Tokens(ctx context.Context) ([]*Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenCols+` FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken marks a token revoked and drops its routes.
func (s *Store) RevokeToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM http_routes WHERE token_id = ?`, id); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM tcp_routes WHERE token_id = ?`, id)
	return err
}

// TouchToken records last use.
func (s *Store) TouchToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tokens SET last_seen_at = ? WHERE id = ?`, now(), id)
	return err
}

// --- routes ---

// UpsertHTTPRoute records ownership of hostname+prefix.
func (s *Store) UpsertHTTPRoute(ctx context.Context, r *HTTPRoute) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO http_routes (hostname, path_prefix, token_id, agent_id, route_id, announced_at, last_online_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hostname, path_prefix) DO UPDATE SET token_id = excluded.token_id, agent_id = excluded.agent_id,
		route_id = excluded.route_id, announced_at = excluded.announced_at, last_online_at = excluded.last_online_at`,
		r.Hostname, r.PathPrefix, r.TokenID, r.AgentID, r.RouteID, now(), now())
	return err
}

// DeleteHTTPRoute removes one hostname+prefix.
func (s *Store) DeleteHTTPRoute(ctx context.Context, hostname, prefix string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM http_routes WHERE hostname = ? AND path_prefix = ?`, hostname, prefix)
	return err
}

// HTTPRoutes lists every stored HTTP route.
func (s *Store) HTTPRoutes(ctx context.Context) ([]*HTTPRoute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hostname, path_prefix, token_id, agent_id, route_id, announced_at, last_online_at FROM http_routes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*HTTPRoute
	for rows.Next() {
		var r HTTPRoute
		var a, o string
		if err := rows.Scan(&r.Hostname, &r.PathPrefix, &r.TokenID, &r.AgentID, &r.RouteID, &a, &o); err != nil {
			return nil, err
		}
		r.AnnouncedAt, r.LastOnlineAt = parseTime(a), parseTime(o)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// UpsertTCPRoute records ownership of a port.
func (s *Store) UpsertTCPRoute(ctx context.Context, r *TCPRoute) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tcp_routes (port, token_id, agent_id, route_id, announced_at, last_online_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(port) DO UPDATE SET token_id = excluded.token_id, agent_id = excluded.agent_id,
		route_id = excluded.route_id, announced_at = excluded.announced_at, last_online_at = excluded.last_online_at`,
		r.Port, r.TokenID, r.AgentID, r.RouteID, now(), now())
	return err
}

// DeleteTCPRoute removes one port.
func (s *Store) DeleteTCPRoute(ctx context.Context, port int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tcp_routes WHERE port = ?`, port)
	return err
}

// TCPRoutes lists every stored TCP route.
func (s *Store) TCPRoutes(ctx context.Context) ([]*TCPRoute, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT port, token_id, agent_id, route_id, announced_at, last_online_at FROM tcp_routes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TCPRoute
	for rows.Next() {
		var r TCPRoute
		var a, o string
		if err := rows.Scan(&r.Port, &r.TokenID, &r.AgentID, &r.RouteID, &a, &o); err != nil {
			return nil, err
		}
		r.AnnouncedAt, r.LastOnlineAt = parseTime(a), parseTime(o)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// MarkOffline stamps last_online_at for every route of an agent.
func (s *Store) MarkOffline(ctx context.Context, tokenID, agentID string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE http_routes SET last_online_at = ? WHERE token_id = ? AND agent_id = ?`, now(), tokenID, agentID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE tcp_routes SET last_online_at = ? WHERE token_id = ? AND agent_id = ?`, now(), tokenID, agentID)
	return err
}

// --- settings ---

// SetSetting stores a key/value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Setting returns a value or "" when unset.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Setting keys persisted by `latch-relay run` for the CLI to reuse.
const (
	SettingPublicHost = "public_host"
	SettingAgentPort  = "agent_port"
	SettingBaseDomain = "base_domain"
)
