package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Postgres is the production Store. Open it with a DSN.
type Postgres struct{ db *sql.DB }

// OpenPostgres connects, pings and migrates.
func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	p := &Postgres{db: db}
	if err := p.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Close() error { return p.db.Close() }

func (p *Postgres) migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
  id text PRIMARY KEY, name text NOT NULL, status text NOT NULL, plan_id text NOT NULL,
  region text NOT NULL, mor_customer_id text, created_at timestamptz NOT NULL,
  suspended_at timestamptz, suspended_reason text, trial_expires_at timestamptz);
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS trial_expires_at timestamptz;
CREATE TABLE IF NOT EXISTS users (
  id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  email text UNIQUE NOT NULL, password_hash text NOT NULL, role text NOT NULL,
  email_verified_at timestamptz, totp_secret text, totp_enabled_at timestamptz,
  created_at timestamptz NOT NULL, last_login_at timestamptz);
CREATE TABLE IF NOT EXISTS sessions (
  id text PRIMARY KEY, user_id text NOT NULL, account_id text NOT NULL, csrf text NOT NULL,
  created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS api_keys (
  id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name text NOT NULL, hash text UNIQUE NOT NULL, prefix text NOT NULL,
  created_at timestamptz NOT NULL, last_used_at timestamptz, revoked_at timestamptz);
CREATE TABLE IF NOT EXISTS projects (
  id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name text NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS tunnel_tokens (
  id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  project_id text, relay_id text NOT NULL, name text NOT NULL, hostnames text, tcp boolean,
  status text NOT NULL, created_at timestamptz NOT NULL, last_seen_at timestamptz);
CREATE TABLE IF NOT EXISTS custom_domains (
  id text PRIMARY KEY, account_id text NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  domain text UNIQUE NOT NULL, relay_id text NOT NULL, status text NOT NULL,
  verified_at timestamptz, created_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS blocklist (pattern text PRIMARY KEY, reason text, created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS audit_log (
  id text PRIMARY KEY, account_id text, actor_user text, actor text NOT NULL, action text NOT NULL,
  target_type text, target_id text, metadata text, ip text, created_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS idx_tokens_account ON tunnel_tokens(account_id);
CREATE INDEX IF NOT EXISTS idx_domains_account ON custom_domains(account_id);
CREATE INDEX IF NOT EXISTS idx_audit_account ON audit_log(account_id, created_at);
CREATE TABLE IF NOT EXISTS usage_rollup (
  account_id text NOT NULL, day date NOT NULL,
  bytes_in bigint NOT NULL DEFAULT 0, bytes_out bigint NOT NULL DEFAULT 0, requests bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, day));
CREATE TABLE IF NOT EXISTS subscriptions (
  account_id text PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  mor_subscription_id text, plan_id text NOT NULL, status text NOT NULL, current_period_end timestamptz);
CREATE TABLE IF NOT EXISTS billing_events (mor_event_id text PRIMARY KEY, received_at timestamptz NOT NULL DEFAULT now());
`

func pgErr(err error) error {
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil && strings.Contains(err.Error(), "duplicate key") {
		return ErrConflict
	}
	return err
}

func hostsJoin(h []string) string { return strings.Join(h, ",") }
func hostsSplit(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func (p *Postgres) CreateAccountWithOwner(ctx context.Context, a Account, u User) (Account, User, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, User{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id,name,status,plan_id,region,created_at,trial_expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.Name, a.Status, a.PlanID, a.Region, a.CreatedAt, a.TrialExpiresAt); err != nil {
		return Account{}, User{}, pgErr(err)
	}
	u.AccountID = a.ID
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,account_id,email,password_hash,role,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		u.ID, u.AccountID, strings.ToLower(u.Email), u.PasswordHash, u.Role, u.CreatedAt); err != nil {
		return Account{}, User{}, pgErr(err)
	}
	return a, u, pgErr(tx.Commit())
}

func (p *Postgres) GetAccount(ctx context.Context, id string) (Account, error) {
	var a Account
	err := p.db.QueryRowContext(ctx, `SELECT id,name,status,plan_id,region,COALESCE(mor_customer_id,''),created_at,suspended_at,COALESCE(suspended_reason,''),trial_expires_at FROM accounts WHERE id=$1`, id).
		Scan(&a.ID, &a.Name, &a.Status, &a.PlanID, &a.Region, &a.MORCustomerID, &a.CreatedAt, &a.SuspendedAt, &a.SuspendedReason, &a.TrialExpiresAt)
	return a, pgErr(err)
}

func (p *Postgres) SetAccountTrial(ctx context.Context, id string, until *time.Time) error {
	_, err := p.db.ExecContext(ctx, `UPDATE accounts SET trial_expires_at=$2 WHERE id=$1`, id, until)
	return err
}

func (p *Postgres) SetAccountStatus(ctx context.Context, id, status, reason string) error {
	var at *time.Time
	if status == AccountSuspended {
		now := time.Now().UTC()
		at = &now
	}
	_, err := p.db.ExecContext(ctx, `UPDATE accounts SET status=$2, suspended_at=$3, suspended_reason=$4 WHERE id=$1`, id, status, at, reason)
	return pgErr(err)
}

func (p *Postgres) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return p.scanUser(p.db.QueryRowContext(ctx, userCols+` WHERE email=$1`, strings.ToLower(email)))
}
func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	return p.scanUser(p.db.QueryRowContext(ctx, userCols+` WHERE id=$1`, id))
}

const userCols = `SELECT id,account_id,email,password_hash,role,email_verified_at,COALESCE(totp_secret,''),totp_enabled_at,created_at,last_login_at FROM users`

type rowScanner interface{ Scan(...any) error }

func (p *Postgres) scanUser(row rowScanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.AccountID, &u.Email, &u.PasswordHash, &u.Role, &u.EmailVerifiedAt, &u.TOTPSecret, &u.TOTPEnabledAt, &u.CreatedAt, &u.LastLoginAt)
	return u, pgErr(err)
}

func (p *Postgres) UpdateUser(ctx context.Context, u User) error {
	_, err := p.db.ExecContext(ctx, `UPDATE users SET totp_secret=$2, totp_enabled_at=$3, email_verified_at=$4, last_login_at=$5 WHERE id=$1`,
		u.ID, nullStr(u.TOTPSecret), u.TOTPEnabledAt, u.EmailVerifiedAt, u.LastLoginAt)
	return pgErr(err)
}

func (p *Postgres) SetUserCredentials(ctx context.Context, userID, email, passwordHash string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE users SET email=$2, password_hash=$3 WHERE id=$1`,
		userID, strings.ToLower(email), passwordHash)
	return pgErr(err)
}

func (p *Postgres) ListUsers(ctx context.Context, accountID string) ([]User, error) {
	rows, err := p.db.QueryContext(ctx, userCols+` WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := p.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateSession(ctx context.Context, s Session) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO sessions (id,user_id,account_id,csrf,created_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		s.ID, s.UserID, s.AccountID, s.CSRF, s.CreatedAt, s.ExpiresAt)
	return pgErr(err)
}
func (p *Postgres) GetSession(ctx context.Context, id string) (Session, error) {
	var s Session
	err := p.db.QueryRowContext(ctx, `SELECT id,user_id,account_id,csrf,created_at,expires_at FROM sessions WHERE id=$1 AND expires_at>now()`, id).
		Scan(&s.ID, &s.UserID, &s.AccountID, &s.CSRF, &s.CreatedAt, &s.ExpiresAt)
	return s, pgErr(err)
}
func (p *Postgres) DeleteSession(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}

func (p *Postgres) CreateAPIKey(ctx context.Context, k APIKey) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO api_keys (id,account_id,name,hash,prefix,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		k.ID, k.AccountID, k.Name, k.Hash, k.Prefix, k.CreatedAt)
	return pgErr(err)
}
func (p *Postgres) GetAPIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	var k APIKey
	err := p.db.QueryRowContext(ctx, `SELECT id,account_id,name,hash,prefix,created_at,last_used_at,revoked_at FROM api_keys WHERE hash=$1 AND revoked_at IS NULL`, hash).
		Scan(&k.ID, &k.AccountID, &k.Name, &k.Hash, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	return k, pgErr(err)
}
func (p *Postgres) ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,account_id,name,hash,prefix,created_at,last_used_at,revoked_at FROM api_keys WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.AccountID, &k.Name, &k.Hash, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (p *Postgres) RevokeAPIKey(ctx context.Context, accountID, id string) error {
	res, err := p.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND account_id=$2`, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreateProject(ctx context.Context, pr Project) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO projects (id,account_id,name,created_at) VALUES ($1,$2,$3,$4)`, pr.ID, pr.AccountID, pr.Name, pr.CreatedAt)
	return pgErr(err)
}
func (p *Postgres) ListProjects(ctx context.Context, accountID string) ([]Project, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,account_id,name,created_at FROM projects WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var pr Project
		if err := rows.Scan(&pr.ID, &pr.AccountID, &pr.Name, &pr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateToken(ctx context.Context, t TunnelToken) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO tunnel_tokens (id,account_id,project_id,relay_id,name,hostnames,tcp,status,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		t.ID, t.AccountID, nullStr(t.ProjectID), t.RelayID, t.Name, hostsJoin(t.Hostnames), t.TCP, t.Status, t.CreatedAt)
	return pgErr(err)
}
func (p *Postgres) ListTokens(ctx context.Context, accountID string) ([]TunnelToken, error) {
	return p.TokensForAccount(ctx, accountID)
}
func (p *Postgres) TokensForAccount(ctx context.Context, accountID string) ([]TunnelToken, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,account_id,COALESCE(project_id,''),relay_id,name,COALESCE(hostnames,''),tcp,status,created_at,last_seen_at FROM tunnel_tokens WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TunnelToken
	for rows.Next() {
		t, hs := TunnelToken{}, ""
		if err := rows.Scan(&t.ID, &t.AccountID, &t.ProjectID, &t.RelayID, &t.Name, &hs, &t.TCP, &t.Status, &t.CreatedAt, &t.LastSeenAt); err != nil {
			return nil, err
		}
		t.Hostnames = hostsSplit(hs)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (p *Postgres) AccountForToken(ctx context.Context, tokenID string) (string, error) {
	var a string
	err := p.db.QueryRowContext(ctx, `SELECT account_id FROM tunnel_tokens WHERE id=$1`, tokenID).Scan(&a)
	return a, pgErr(err)
}

func (p *Postgres) GetToken(ctx context.Context, accountID, id string) (TunnelToken, error) {
	t, hs := TunnelToken{}, ""
	err := p.db.QueryRowContext(ctx, `SELECT id,account_id,COALESCE(project_id,''),relay_id,name,COALESCE(hostnames,''),tcp,status,created_at,last_seen_at FROM tunnel_tokens WHERE id=$1 AND account_id=$2`, id, accountID).
		Scan(&t.ID, &t.AccountID, &t.ProjectID, &t.RelayID, &t.Name, &hs, &t.TCP, &t.Status, &t.CreatedAt, &t.LastSeenAt)
	t.Hostnames = hostsSplit(hs)
	return t, pgErr(err)
}
func (p *Postgres) SetTokenStatus(ctx context.Context, accountID, id, status string) error {
	res, err := p.db.ExecContext(ctx, `UPDATE tunnel_tokens SET status=$3 WHERE id=$1 AND account_id=$2`, id, accountID, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreateDomain(ctx context.Context, d CustomDomain) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO custom_domains (id,account_id,domain,relay_id,status,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		d.ID, d.AccountID, d.Domain, d.RelayID, d.Status, d.CreatedAt)
	return pgErr(err)
}
func (p *Postgres) ListDomains(ctx context.Context, accountID string) ([]CustomDomain, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,account_id,domain,relay_id,status,verified_at,created_at FROM custom_domains WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomDomain
	for rows.Next() {
		var d CustomDomain
		if err := rows.Scan(&d.ID, &d.AccountID, &d.Domain, &d.RelayID, &d.Status, &d.VerifiedAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (p *Postgres) GetDomain(ctx context.Context, accountID, id string) (CustomDomain, error) {
	var d CustomDomain
	err := p.db.QueryRowContext(ctx, `SELECT id,account_id,domain,relay_id,status,verified_at,created_at FROM custom_domains WHERE id=$1 AND account_id=$2`, id, accountID).
		Scan(&d.ID, &d.AccountID, &d.Domain, &d.RelayID, &d.Status, &d.VerifiedAt, &d.CreatedAt)
	return d, pgErr(err)
}
func (p *Postgres) SetDomainVerified(ctx context.Context, accountID, id string, when time.Time) error {
	res, err := p.db.ExecContext(ctx, `UPDATE custom_domains SET status='verified', verified_at=$3 WHERE id=$1 AND account_id=$2`, id, accountID, when)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (p *Postgres) DeleteDomain(ctx context.Context, accountID, id string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM custom_domains WHERE id=$1 AND account_id=$2`, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) AddBlocklist(ctx context.Context, pattern, reason string) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO blocklist (pattern,reason) VALUES ($1,$2) ON CONFLICT (pattern) DO UPDATE SET reason=excluded.reason`, strings.ToLower(pattern), reason)
	return err
}
func (p *Postgres) IsBlocked(ctx context.Context, domain string) (bool, error) {
	d := strings.ToLower(domain)
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM blocklist WHERE pattern=$1 OR $1 LIKE '%.'||pattern`, d).Scan(&n)
	return n > 0, err
}
func (p *Postgres) AppendAudit(ctx context.Context, e AuditEntry) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO audit_log (id,account_id,actor_user,actor,action,target_type,target_id,metadata,ip,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.ID, nullStr(e.AccountID), nullStr(e.ActorUser), e.Actor, e.Action, nullStr(e.TargetType), nullStr(e.TargetID), nullStr(e.Metadata), nullStr(e.IP), e.CreatedAt)
	return err
}
func (p *Postgres) ListAudit(ctx context.Context, accountID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `SELECT id,COALESCE(account_id,''),COALESCE(actor_user,''),actor,action,COALESCE(target_type,''),COALESCE(target_id,''),COALESCE(metadata,''),COALESCE(ip,''),created_at FROM audit_log WHERE account_id=$1 ORDER BY created_at DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.ActorUser, &e.Actor, &e.Action, &e.TargetType, &e.TargetID, &e.Metadata, &e.IP, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (p *Postgres) SetAccountPlan(ctx context.Context, accountID, planID string) error {
	res, err := p.db.ExecContext(ctx, `UPDATE accounts SET plan_id=$2 WHERE id=$1`, accountID, planID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) AddUsage(ctx context.Context, accountID string, day time.Time, bin, bout, reqs int64) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO usage_rollup (account_id,day,bytes_in,bytes_out,requests)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (account_id,day) DO UPDATE SET bytes_in=usage_rollup.bytes_in+$3, bytes_out=usage_rollup.bytes_out+$4, requests=usage_rollup.requests+$5`,
		accountID, day.UTC(), bin, bout, reqs)
	return err
}

func (p *Postgres) MonthUsage(ctx context.Context, accountID string, monthStart time.Time) (int64, int64, int64, error) {
	var bin, bout, reqs int64
	err := p.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes_in),0),COALESCE(SUM(bytes_out),0),COALESCE(SUM(requests),0)
		FROM usage_rollup WHERE account_id=$1 AND day >= $2`, accountID, monthStart.UTC()).Scan(&bin, &bout, &reqs)
	return bin, bout, reqs, err
}

func (p *Postgres) ListAllAccounts(ctx context.Context) ([]Account, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,name,status,plan_id,region,COALESCE(mor_customer_id,''),created_at,suspended_at,COALESCE(suspended_reason,''),trial_expires_at FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Status, &a.PlanID, &a.Region, &a.MORCustomerID, &a.CreatedAt, &a.SuspendedAt, &a.SuspendedReason, &a.TrialExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) SetSubscription(ctx context.Context, sub Subscription) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO subscriptions (account_id,mor_subscription_id,plan_id,status,current_period_end)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (account_id) DO UPDATE SET mor_subscription_id=excluded.mor_subscription_id, plan_id=excluded.plan_id, status=excluded.status, current_period_end=excluded.current_period_end`,
		sub.AccountID, nullStr(sub.MORSubID), sub.PlanID, sub.Status, sub.CurrentPeriodEnd)
	return err
}

func (p *Postgres) GetSubscription(ctx context.Context, accountID string) (Subscription, error) {
	var sub Subscription
	err := p.db.QueryRowContext(ctx, `SELECT account_id,COALESCE(mor_subscription_id,''),plan_id,status,COALESCE(current_period_end,now()) FROM subscriptions WHERE account_id=$1`, accountID).
		Scan(&sub.AccountID, &sub.MORSubID, &sub.PlanID, &sub.Status, &sub.CurrentPeriodEnd)
	return sub, pgErr(err)
}

func (p *Postgres) RecordBillingEvent(ctx context.Context, eventID string) (bool, error) {
	res, err := p.db.ExecContext(ctx, `INSERT INTO billing_events (mor_event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (p *Postgres) ExportAccount(ctx context.Context, accountID string) (map[string]any, error) {
	a, err := p.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	users, _ := p.ListUsers(ctx, accountID)
	for i := range users {
		users[i].PasswordHash, users[i].TOTPSecret = "", ""
	}
	toks, _ := p.TokensForAccount(ctx, accountID)
	doms, _ := p.ListDomains(ctx, accountID)
	aud, _ := p.ListAudit(ctx, accountID, 1000)
	return map[string]any{"account": a, "users": users, "tokens": toks, "domains": doms, "audit": aud}, nil
}

func (p *Postgres) DeleteAccount(ctx context.Context, accountID string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, accountID) // cascades
	return err
}

var _ Store = (*Postgres)(nil)
