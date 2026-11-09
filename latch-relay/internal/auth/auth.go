// Package auth issues and verifies agent tokens. A token looks like
// "lt_<id>_<secret>"; the store keeps only an argon2id hash of the secret and
// the plain token is shown once at creation. Scopes limit which hostnames a
// token may publish.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/kayaramazan/latch/latch-relay/internal/store"
	"github.com/kayaramazan/latch/proto/model"
	"github.com/kayaramazan/latch/proto/tunnel"
)

// Errors returned by Verify.
var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrRevoked      = errors.New("auth: token revoked")
	ErrExpired      = errors.New("auth: token expired")
)

// argon2id parameters: one pass, 64 MiB, 4 lanes. Verification runs once per
// agent connection.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

const tokenPrefix = "lt_"

// Manager issues and verifies tokens against the store.
type Manager struct {
	st  *store.Store
	now func() time.Time
}

// New returns a Manager.
func New(st *store.Store) *Manager {
	return &Manager{st: st, now: time.Now}
}

// Create issues a new token and returns the plain token together with the
// stored record.
func (m *Manager) Create(ctx context.Context, name string, scope tunnel.Scope, expires *time.Time) (string, *store.Token, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("auth: token name is required")
	}
	scope = NormalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return "", nil, err
	}
	id := randomString(8)
	secret := randomString(32)
	hash, err := hashSecret(secret)
	if err != nil {
		return "", nil, err
	}
	t := &store.Token{ID: id, Name: name, Hash: hash, Scope: scope, CreatedAt: m.now().UTC(), ExpiresAt: expires}
	if err := m.st.InsertToken(ctx, t); err != nil {
		return "", nil, err
	}
	return tokenPrefix + id + "_" + secret, t, nil
}

// Verify checks a plain token and returns its record. Unknown, malformed and
// wrong-secret tokens all return ErrInvalidToken.
func (m *Manager) Verify(ctx context.Context, plain string) (*store.Token, error) {
	id, secret, ok := split(plain)
	if !ok {
		return nil, ErrInvalidToken
	}
	t, err := m.st.Token(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = hashSecret(secret) // keep timing similar for unknown ids
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !verifySecret(t.Hash, secret) {
		return nil, ErrInvalidToken
	}
	if t.RevokedAt != nil {
		return nil, ErrRevoked
	}
	if t.ExpiresAt != nil && m.now().After(*t.ExpiresAt) {
		return nil, ErrExpired
	}
	_ = m.st.TouchToken(ctx, t.ID)
	return t, nil
}

// Revoke revokes a token and drops its routes.
func (m *Manager) Revoke(ctx context.Context, id string) error {
	return m.st.RevokeToken(ctx, id)
}

// List returns all tokens.
func (m *Manager) List(ctx context.Context) ([]*store.Token, error) {
	return m.st.Tokens(ctx)
}

func split(plain string) (id, secret string, ok bool) {
	rest, found := strings.CutPrefix(plain, tokenPrefix)
	if !found {
		return "", "", false
	}
	i := strings.IndexByte(rest, '_')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// tokenEncoding has no "_" so it never collides with the "lt_<id>_<secret>"
// delimiters, and no "-" or padding so tokens are clean word characters.
var tokenEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return tokenEncoding.EncodeToString(b)
}

func hashSecret(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifySecret(encoded, secret string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, tm uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &tm, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, tm, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidateScope checks hostname patterns (lowercase FQDNs, optionally with a
// single leading "*." label) and the optional TCP port range.
func ValidateScope(s tunnel.Scope) error {
	if len(s.Hostnames) == 0 && !s.TCP {
		return fmt.Errorf("auth: scope must allow at least one hostname or tcp")
	}
	for _, p := range s.Hostnames {
		if err := ValidatePattern(p); err != nil {
			return err
		}
	}
	if len(s.TCPPortRange) != 0 && (len(s.TCPPortRange) != 2 || s.TCPPortRange[0] < 1 || s.TCPPortRange[1] > 65535 || s.TCPPortRange[0] > s.TCPPortRange[1]) {
		return fmt.Errorf("auth: tcpPortRange must be [min, max]")
	}
	return nil
}

// ValidatePattern checks one scope entry.
func ValidatePattern(p string) error {
	base := strings.TrimPrefix(p, "*.")
	if strings.Contains(base, "*") {
		return fmt.Errorf("auth: %q: wildcard only allowed as the leftmost label", p)
	}
	if err := model.ValidateHostname(base); err != nil {
		return fmt.Errorf("auth: %q: %w", p, err)
	}
	return nil
}

// ScopeAllows reports whether hostname is covered by the scope. "*.x.y"
// matches exactly one extra label, like a wildcard certificate.
func ScopeAllows(s tunnel.Scope, hostname string) bool {
	hostname = strings.ToLower(hostname)
	for _, p := range s.Hostnames {
		if p == hostname {
			return true
		}
		if base, ok := strings.CutPrefix(p, "*."); ok {
			if rest, ok := strings.CutSuffix(hostname, "."+base); ok && rest != "" && !strings.Contains(rest, ".") {
				return true
			}
		}
	}
	return false
}

// NormalizeScope lowercases, trims and de-duplicates hostnames.
func NormalizeScope(s tunnel.Scope) tunnel.Scope {
	seen := map[string]bool{}
	out := tunnel.Scope{TCP: s.TCP, TCPPortRange: s.TCPPortRange, Hostnames: []string{}}
	for _, h := range s.Hostnames {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out.Hostnames = append(out.Hostnames, h)
	}
	return out
}
