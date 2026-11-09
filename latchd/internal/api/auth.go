package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/kayaramazan/latch/proto/model"
)

// Session lifetime (ROADMAP decision point).
const sessionTTL = 24 * time.Hour

const (
	sessionCookie = "latch_session"
	csrfHeader    = "X-CSRF-Token"
)

// argon2id parameters for the management password.
const (
	pwTime    = 1
	pwMemory  = 64 * 1024
	pwThreads = 4
	pwKeyLen  = 32
)

// HashPassword returns an argon2id encoding of password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, pwTime, pwMemory, pwThreads, pwKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", pwMemory, pwTime, pwThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword checks password against an argon2id encoding.
func verifyPassword(encoded, password string) bool {
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
	got := argon2.IDKey([]byte(password), salt, uint32(tm), uint32(mem), uint8(th), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// sessionStore holds active sessions in memory.
type sessionStore struct {
	mu   sync.Mutex
	sess map[string]sessionEntry
}

type sessionEntry struct {
	csrf    string
	expires time.Time
}

func newSessionStore() *sessionStore { return &sessionStore{sess: map[string]sessionEntry{}} }

func (s *sessionStore) create() (token, csrf string) {
	token, csrf = randToken(), randToken()
	s.mu.Lock()
	s.sess[token] = sessionEntry{csrf: csrf, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	return token, csrf
}

func (s *sessionStore) get(token string) (sessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sess[token]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(s.sess, token)
		}
		return sessionEntry{}, false
	}
	return e, true
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sess, token)
	s.mu.Unlock()
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// loginLimiter is a simple per-IP attempt limiter for the login endpoint.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{attempts: map[string][]time.Time{}} }

// allow reports whether ip may attempt a login now (max 5 / minute).
func (l *loginLimiter) allow(ip string) bool {
	const max, window = 5, time.Minute
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-window)
	kept := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.attempts[ip] = kept
		return false
	}
	l.attempts[ip] = append(kept, now)
	return true
}

// authEnabled reports whether the current config requires authentication.
func (s *Server) authEnabled() bool {
	snap, _ := s.store.Current()
	return snap.Settings.Auth.Mode == model.AuthPassword && snap.Settings.Auth.PasswordHash != ""
}

// --- handlers ---

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.logins.allow(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many login attempts; wait a minute")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	snap, _ := s.store.Current()
	hash := snap.Settings.Auth.PasswordHash
	if hash == "" || !verifyPassword(hash, in.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid password")
		return
	}
	token, csrf := s.sessions.create()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]string{"csrfToken": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": !s.authEnabled() || s.validSession(r) != "",
		"authRequired":  s.authEnabled(),
	})
}

// validSession returns the session's CSRF token if the request carries a valid
// session cookie, else "".
func (s *Server) validSession(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	e, ok := s.sessions.get(c.Value)
	if !ok {
		return ""
	}
	return e.csrf
}

// guard wraps the mux with auth, CSRF and origin checks.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cross-origin CSRF defense for state-changing methods: a browser sends
		// Origin; reject it unless it is same-origin (localhost). The CLI sends
		// no Origin and is unaffected.
		if isMutating(r.Method) {
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) {
				writeErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		// Open endpoints.
		p := r.URL.Path
		if p == "/api/auth/login" || p == "/api/system" || p == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authEnabled() {
			next.ServeHTTP(w, r)
			return
		}
		csrf := s.validSession(r)
		if csrf == "" {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if isMutating(r.Method) && subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get(csrfHeader))) != 1 {
			writeErr(w, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func sameOrigin(origin, host string) bool {
	// origin is like "http://127.0.0.1:4040"; compare host:port to r.Host.
	i := strings.Index(origin, "://")
	if i < 0 {
		return false
	}
	oh := origin[i+3:]
	return oh == host || hostOf(oh) == "localhost" || hostOf(oh) == "127.0.0.1"
}

func hostOf(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
