package store

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Store for tests and the dev skeleton. It enforces the
// same account scoping as the PostgreSQL store.
type Memory struct {
	mu        sync.Mutex
	accounts  map[string]Account
	users     map[string]User
	emailIdx  map[string]string // email -> userID
	sessions  map[string]Session
	apiKeys   map[string]APIKey
	keyHash   map[string]string // hash -> keyID
	projects  map[string]Project
	tokens    map[string]TunnelToken
	domains   map[string]CustomDomain
	blocklist []blockEntry
	audit     []AuditEntry
}

type blockEntry struct{ pattern, reason string }

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		accounts: map[string]Account{}, users: map[string]User{}, emailIdx: map[string]string{},
		sessions: map[string]Session{}, apiKeys: map[string]APIKey{}, keyHash: map[string]string{},
		projects: map[string]Project{}, tokens: map[string]TunnelToken{}, domains: map[string]CustomDomain{},
	}
}

func (m *Memory) Close() error { return nil }

func (m *Memory) CreateAccountWithOwner(_ context.Context, acct Account, owner User) (Account, User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.emailIdx[strings.ToLower(owner.Email)]; ok {
		return Account{}, User{}, ErrConflict
	}
	m.accounts[acct.ID] = acct
	owner.AccountID = acct.ID
	m.users[owner.ID] = owner
	m.emailIdx[strings.ToLower(owner.Email)] = owner.ID
	return acct, owner, nil
}

func (m *Memory) GetAccount(_ context.Context, id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (m *Memory) SetAccountStatus(_ context.Context, id, status, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = status
	a.SuspendedReason = reason
	if status == AccountSuspended {
		now := time.Now().UTC()
		a.SuspendedAt = &now
	} else {
		a.SuspendedAt = nil
	}
	m.accounts[id] = a
	return nil
}

func (m *Memory) GetUserByEmail(_ context.Context, email string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.emailIdx[strings.ToLower(email)]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}

func (m *Memory) GetUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) UpdateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; !ok {
		return ErrNotFound
	}
	m.users[u.ID] = u
	return nil
}

func (m *Memory) ListUsers(_ context.Context, accountID string) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []User
	for _, u := range m.users {
		if u.AccountID == accountID {
			out = append(out, u)
		}
	}
	return out, nil
}

func (m *Memory) CreateSession(_ context.Context, s Session) error {
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (m *Memory) GetSession(_ context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || time.Now().After(s.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return s, nil
}

func (m *Memory) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *Memory) CreateAPIKey(_ context.Context, k APIKey) error {
	m.mu.Lock()
	m.apiKeys[k.ID] = k
	m.keyHash[k.Hash] = k.ID
	m.mu.Unlock()
	return nil
}

func (m *Memory) GetAPIKeyByHash(_ context.Context, hash string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.keyHash[hash]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	k := m.apiKeys[id]
	if k.RevokedAt != nil {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (m *Memory) ListAPIKeys(_ context.Context, accountID string) ([]APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIKey
	for _, k := range m.apiKeys {
		if k.AccountID == accountID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *Memory) RevokeAPIKey(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok || k.AccountID != accountID {
		return ErrNotFound
	}
	now := time.Now().UTC()
	k.RevokedAt = &now
	m.apiKeys[id] = k
	return nil
}

func (m *Memory) CreateProject(_ context.Context, p Project) error {
	m.mu.Lock()
	m.projects[p.ID] = p
	m.mu.Unlock()
	return nil
}

func (m *Memory) ListProjects(_ context.Context, accountID string) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Project
	for _, p := range m.projects {
		if p.AccountID == accountID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Memory) CreateToken(_ context.Context, t TunnelToken) error {
	m.mu.Lock()
	m.tokens[t.ID] = t
	m.mu.Unlock()
	return nil
}

func (m *Memory) ListTokens(_ context.Context, accountID string) ([]TunnelToken, error) {
	return m.TokensForAccount(context.Background(), accountID)
}

func (m *Memory) TokensForAccount(_ context.Context, accountID string) ([]TunnelToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TunnelToken
	for _, t := range m.tokens {
		if t.AccountID == accountID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *Memory) GetToken(_ context.Context, accountID, id string) (TunnelToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.AccountID != accountID {
		return TunnelToken{}, ErrNotFound
	}
	return t, nil
}

func (m *Memory) SetTokenStatus(_ context.Context, accountID, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.AccountID != accountID {
		return ErrNotFound
	}
	t.Status = status
	m.tokens[id] = t
	return nil
}

func (m *Memory) CreateDomain(_ context.Context, d CustomDomain) error {
	m.mu.Lock()
	m.domains[d.ID] = d
	m.mu.Unlock()
	return nil
}

func (m *Memory) ListDomains(_ context.Context, accountID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		if d.AccountID == accountID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *Memory) GetDomain(_ context.Context, accountID, id string) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok || d.AccountID != accountID {
		return CustomDomain{}, ErrNotFound
	}
	return d, nil
}

func (m *Memory) SetDomainVerified(_ context.Context, accountID, id string, when time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok || d.AccountID != accountID {
		return ErrNotFound
	}
	d.Status = "verified"
	d.VerifiedAt = &when
	m.domains[id] = d
	return nil
}

func (m *Memory) DeleteDomain(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[id]
	if !ok || d.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.domains, id)
	return nil
}

func (m *Memory) AddBlocklist(_ context.Context, pattern, reason string) error {
	m.mu.Lock()
	m.blocklist = append(m.blocklist, blockEntry{pattern: strings.ToLower(pattern), reason: reason})
	m.mu.Unlock()
	return nil
}

func (m *Memory) IsBlocked(_ context.Context, domain string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := strings.ToLower(domain)
	for _, b := range m.blocklist {
		if d == b.pattern || strings.HasSuffix(d, "."+b.pattern) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Memory) AppendAudit(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	m.audit = append(m.audit, e)
	m.mu.Unlock()
	return nil
}

func (m *Memory) ListAudit(_ context.Context, accountID string, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditEntry
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		if m.audit[i].AccountID == accountID {
			out = append(out, m.audit[i])
		}
	}
	return out, nil
}

var _ Store = (*Memory)(nil)
