// Package store is latch-control's persistence: domain types, a Store
// interface, an in-memory implementation (tests + dev) and a PostgreSQL
// implementation (production). Every tenant operation is scoped by account id.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a row does not exist (or is not visible to the
// caller's account — the two are indistinguishable on purpose, for isolation).
var ErrNotFound = errors.New("control store: not found")

// ErrConflict is returned on a uniqueness violation (e.g. duplicate email).
var ErrConflict = errors.New("control store: conflict")

// Account statuses.
const (
	AccountActive    = "active"
	AccountSuspended = "suspended"
	AccountClosed    = "closed"
)

// Roles.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Account is a tenant organization.
type Account struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	PlanID          string     `json:"planId"`
	Region          string     `json:"region"`
	MORCustomerID   string     `json:"morCustomerId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	SuspendedAt     *time.Time `json:"suspendedAt,omitempty"`
	SuspendedReason string     `json:"suspendedReason,omitempty"`
}

// User is a member of an account.
type User struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"accountId"`
	Email           string     `json:"email"`
	PasswordHash    string     `json:"-"`
	Role            string     `json:"role"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	TOTPSecret      string     `json:"-"`
	TOTPEnabledAt   *time.Time `json:"totpEnabledAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	LastLoginAt     *time.Time `json:"lastLoginAt,omitempty"`
}

// Session is a logged-in browser session.
type Session struct {
	ID        string    `json:"-"` // the cookie value
	UserID    string    `json:"userId"`
	AccountID string    `json:"accountId"`
	CSRF      string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// APIKey is a programmatic credential for an account.
type APIKey struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"accountId"`
	Name       string     `json:"name"`
	Hash       string     `json:"-"`
	Prefix     string     `json:"prefix"` // shown to the user to identify the key
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// Project groups an account's resources.
type Project struct {
	ID        string    `json:"id"`
	AccountID string    `json:"accountId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// TunnelToken is an account's token metadata (the secret lives on the relay).
type TunnelToken struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"accountId"`
	ProjectID  string     `json:"projectId"`
	RelayID    string     `json:"relayId"`
	Name       string     `json:"name"`
	Hostnames  []string   `json:"hostnames,omitempty"`
	TCP        bool       `json:"tcp,omitempty"`
	Status     string     `json:"status"` // active | revoked
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// CustomDomain is an account's registered external hostname.
type CustomDomain struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"accountId"`
	Domain     string     `json:"domain"`
	RelayID    string     `json:"relayId"`
	Status     string     `json:"status"` // pending | verified | failed
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// AuditEntry records an action.
type AuditEntry struct {
	ID         string    `json:"id"`
	AccountID  string    `json:"accountId,omitempty"`
	ActorUser  string    `json:"actorUserId,omitempty"`
	Actor      string    `json:"actor"` // user | system | mor
	Action     string    `json:"action"`
	TargetType string    `json:"targetType,omitempty"`
	TargetID   string    `json:"targetId,omitempty"`
	Metadata   string    `json:"metadata,omitempty"`
	IP         string    `json:"ip,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Store is latch-control's persistence contract.
type Store interface {
	// Accounts & users.
	CreateAccountWithOwner(ctx context.Context, acct Account, owner User) (Account, User, error)
	GetAccount(ctx context.Context, id string) (Account, error)
	SetAccountStatus(ctx context.Context, id, status, reason string) error
	GetUserByEmail(ctx context.Context, email string) (User, error)
	GetUser(ctx context.Context, id string) (User, error)
	UpdateUser(ctx context.Context, u User) error
	ListUsers(ctx context.Context, accountID string) ([]User, error)

	// Sessions & API keys.
	CreateSession(ctx context.Context, s Session) error
	GetSession(ctx context.Context, id string) (Session, error)
	DeleteSession(ctx context.Context, id string) error
	CreateAPIKey(ctx context.Context, k APIKey) error
	GetAPIKeyByHash(ctx context.Context, hash string) (APIKey, error)
	ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, accountID, id string) error

	// Projects.
	CreateProject(ctx context.Context, p Project) error
	ListProjects(ctx context.Context, accountID string) ([]Project, error)

	// Tenant resources (all scoped by accountID).
	CreateToken(ctx context.Context, t TunnelToken) error
	ListTokens(ctx context.Context, accountID string) ([]TunnelToken, error)
	GetToken(ctx context.Context, accountID, id string) (TunnelToken, error)
	SetTokenStatus(ctx context.Context, accountID, id, status string) error
	TokensForAccount(ctx context.Context, accountID string) ([]TunnelToken, error)

	CreateDomain(ctx context.Context, d CustomDomain) error
	ListDomains(ctx context.Context, accountID string) ([]CustomDomain, error)
	GetDomain(ctx context.Context, accountID, id string) (CustomDomain, error)
	SetDomainVerified(ctx context.Context, accountID, id string, when time.Time) error
	DeleteDomain(ctx context.Context, accountID, id string) error

	// Abuse & audit.
	AddBlocklist(ctx context.Context, pattern, reason string) error
	IsBlocked(ctx context.Context, domain string) (bool, error)
	AppendAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, accountID string, limit int) ([]AuditEntry, error)

	Close() error
}
