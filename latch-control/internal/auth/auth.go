// Package auth is latch-control's credential handling: argon2id passwords,
// TOTP MFA, random session/CSRF/API-key tokens and API-key hashing.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

const (
	pwTime    = 1
	pwMemory  = 64 * 1024
	pwThreads = 4
	pwKeyLen  = 32
)

// HashPassword returns an argon2id encoding.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, pwTime, pwMemory, pwThreads, pwKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", pwMemory, pwTime, pwThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an argon2id encoding.
func VerifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, tm, th int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &tm, &th); err != nil {
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
	got := argon2.IDKey([]byte(pw), salt, uint32(tm), uint32(mem), uint8(th), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Token returns a random hex token (for sessions, CSRF).
func Token() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// APIKey generates a display key ("lk_<id>_<secret>") and its storage hash.
func APIKey() (plain, prefix, hash string) {
	id := randString(8)
	secret := randString(32)
	plain = "lk_" + id + "_" + secret
	prefix = "lk_" + id
	hash = HashAPIKey(plain)
	return
}

// HashAPIKey hashes an API key for storage/lookup (fast; the key has high entropy).
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// NewTOTPSecret generates a TOTP secret and its otpauth URL for the account.
func NewTOTPSecret(issuer, account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP checks a 6-digit code against a secret.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}
