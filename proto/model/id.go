package model

import (
	"crypto/rand"
	"encoding/base32"
	"regexp"
	"strings"
)

// ID prefixes for each object kind. IDs look like "rt_7f3kq2…" and are never
// user-editable.
const (
	PrefixRoute     = "rt"
	PrefixIPPolicy  = "pol"
	PrefixRateLimit = "rl"
	PrefixRelay     = "rly"
	PrefixAgent     = "ag"
)

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

var idPattern = regexp.MustCompile(`^[a-z]{2,4}_[a-z2-7]{26}$`)

// NewID returns a new random identifier with the given prefix: 16 random
// bytes, base32-lowercase (26 chars).
func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("model: crypto/rand failed: " + err.Error())
	}
	return prefix + "_" + idEncoding.EncodeToString(b[:])
}

// ValidID reports whether id is well-formed and carries the expected prefix.
func ValidID(id, prefix string) bool {
	return idPattern.MatchString(id) && strings.HasPrefix(id, prefix+"_")
}
