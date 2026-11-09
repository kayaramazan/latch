package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/kayaramazan/latch/proto/tunnel"
)

// PolicyRecord is a token's persisted policy snapshot.
type PolicyRecord struct {
	TokenID  string
	Version  int
	Snapshot tunnel.PolicySync
}

// SavePolicy persists (replaces) a token's policy snapshot.
func (s *Store) SavePolicy(ctx context.Context, tokenID string, ps *tunnel.PolicySync) error {
	raw, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO policies (token_id, version, snapshot, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(token_id) DO UPDATE SET version = excluded.version, snapshot = excluded.snapshot, updated_at = excluded.updated_at`,
		tokenID, ps.Version, string(raw), now())
	return err
}

// Policy returns a token's persisted policy, or ok=false when none exists.
func (s *Store) Policy(ctx context.Context, tokenID string) (PolicyRecord, bool, error) {
	var version int
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT version, snapshot FROM policies WHERE token_id = ?`, tokenID).Scan(&version, &raw)
	if err == sql.ErrNoRows {
		return PolicyRecord{}, false, nil
	}
	if err != nil {
		return PolicyRecord{}, false, err
	}
	var ps tunnel.PolicySync
	if err := json.Unmarshal([]byte(raw), &ps); err != nil {
		return PolicyRecord{}, false, err
	}
	return PolicyRecord{TokenID: tokenID, Version: version, Snapshot: ps}, true, nil
}

// AllPolicies returns every persisted policy snapshot (for startup rebuild).
func (s *Store) AllPolicies(ctx context.Context) ([]PolicyRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token_id, version, snapshot FROM policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRecord
	for rows.Next() {
		var rec PolicyRecord
		var raw string
		if err := rows.Scan(&rec.TokenID, &rec.Version, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &rec.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeletePolicy removes a token's policy (e.g. after revocation).
func (s *Store) DeletePolicy(ctx context.Context, tokenID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE token_id = ?`, tokenID)
	return err
}
