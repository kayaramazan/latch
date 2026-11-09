package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// CustomDomain is an operator-registered external hostname that the relay may
// obtain an on-demand certificate for (once its DNS points here).
type CustomDomain struct {
	Domain     string     `json:"domain"`
	Target     string     `json:"target"`
	CreatedAt  time.Time  `json:"createdAt"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	Note       string     `json:"note,omitempty"`
}

func normalizeDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// AddCustomDomain registers (or updates the target/note of) a custom domain.
func (s *Store) AddCustomDomain(ctx context.Context, domain, target, note string) error {
	domain = normalizeDomain(domain)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO custom_domains (domain, target, created_at, note) VALUES (?, ?, ?, ?)
		 ON CONFLICT(domain) DO UPDATE SET target = excluded.target, note = excluded.note`,
		domain, normalizeDomain(target), now(), note)
	return err
}

// RemoveCustomDomain deletes a custom domain. It returns ErrNotFound when the
// domain was not registered.
func (s *Store) RemoveCustomDomain(ctx context.Context, domain string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_domains WHERE domain = ?`, normalizeDomain(domain))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCustomDomainVerified records a successful DNS verification.
func (s *Store) SetCustomDomainVerified(ctx context.Context, domain string, when time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE custom_domains SET verified_at = ? WHERE domain = ?`,
		when.UTC().Format(time.RFC3339Nano), normalizeDomain(domain))
	return err
}

// GetCustomDomain returns one registered domain.
func (s *Store) GetCustomDomain(ctx context.Context, domain string) (CustomDomain, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT domain, target, created_at, verified_at, note FROM custom_domains WHERE domain = ?`,
		normalizeDomain(domain))
	return scanCustomDomain(row)
}

// ListCustomDomains returns all registered domains ordered by name.
func (s *Store) ListCustomDomains(ctx context.Context) ([]CustomDomain, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT domain, target, created_at, verified_at, note FROM custom_domains ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomDomain
	for rows.Next() {
		d, err := scanCustomDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// IsRegistered reports whether domain is registered. It is safe to call on the
// TLS handshake path (indexed primary-key lookup) and reads the database live,
// so domains added by the CLI are visible without restarting the relay.
func (s *Store) IsRegistered(domain string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM custom_domains WHERE domain = ?`, normalizeDomain(domain)).Scan(&one)
	return err == nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanCustomDomain(sc scanner) (CustomDomain, error) {
	var d CustomDomain
	var created string
	var verified sql.NullString
	if err := sc.Scan(&d.Domain, &d.Target, &created, &verified, &d.Note); err != nil {
		return CustomDomain{}, err
	}
	d.CreatedAt = parseTime(created)
	d.VerifiedAt = parseTimePtr(verified)
	return d, nil
}
