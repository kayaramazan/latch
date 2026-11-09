// Package app is latch-control's customer-facing control plane: signup/login
// (password + TOTP), projects, self-service tokens and custom domains (placed
// on the relay fleet), tenant isolation, suspend, API keys and audit.
package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/auth"
	"github.com/kayaramazan/latch/latch-control/internal/store"
	"github.com/kayaramazan/latch/proto/control"
)

// Fleet is the placement surface (implemented by plane.Plane).
type Fleet interface {
	PickRelay(region string) (string, bool)
	RelayFor(relayID string) (*control.Client, string, bool)
}

// Options configure the Server.
type Options struct {
	Store    store.Store
	Fleet    Fleet
	Issuer   string // TOTP issuer label, e.g. "Latch"
	OpsToken string // bearer for /ops endpoints
	Region   string // default region for new accounts
	Logger   *slog.Logger
}

// Server is the customer control plane.
type Server struct {
	st    store.Store
	fleet Fleet
	mux   *http.ServeMux
	opts  Options
	log   *slog.Logger
}

// New builds the Server.
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Issuer == "" {
		o.Issuer = "Latch"
	}
	if o.Region == "" {
		o.Region = "eu"
	}
	s := &Server{st: o.Store, fleet: o.Fleet, mux: http.NewServeMux(), opts: o, log: o.Logger}
	s.routes()
	return s
}

// Handler returns the guarded mux.
func (s *Server) Handler() http.Handler { return s.guard(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/signup", s.signup)
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/logout", s.logout)
	s.mux.HandleFunc("GET /api/me", s.me)
	s.mux.HandleFunc("POST /api/mfa/setup", s.mfaSetup)
	s.mux.HandleFunc("POST /api/mfa/enable", s.mfaEnable)
	s.mux.HandleFunc("GET /api/projects", s.listProjects)
	s.mux.HandleFunc("POST /api/projects", s.createProject)
	s.mux.HandleFunc("GET /api/tokens", s.listTokens)
	s.mux.HandleFunc("POST /api/tokens", s.createToken)
	s.mux.HandleFunc("DELETE /api/tokens/{id}", s.revokeToken)
	s.mux.HandleFunc("GET /api/domains", s.listDomains)
	s.mux.HandleFunc("POST /api/domains", s.createDomain)
	s.mux.HandleFunc("POST /api/domains/{id}/verify", s.verifyDomain)
	s.mux.HandleFunc("DELETE /api/domains/{id}", s.deleteDomain)
	s.mux.HandleFunc("GET /api/apikeys", s.listKeys)
	s.mux.HandleFunc("POST /api/apikeys", s.createKey)
	s.mux.HandleFunc("DELETE /api/apikeys/{id}", s.revokeKey)
	s.mux.HandleFunc("GET /api/audit", s.audit)
	s.mux.HandleFunc("POST /ops/accounts/{id}/suspend", s.opsSuspend)
	s.mux.HandleFunc("POST /ops/accounts/{id}/unsuspend", s.opsUnsuspend)
	s.mux.HandleFunc("POST /ops/blocklist", s.opsBlocklist)
}

// --- auth / principal ---

type principal struct {
	AccountID string
	UserID    string
	CSRF      string
	ViaAPIKey bool
}

type ctxKey struct{}

func (s *Server) principalOf(r *http.Request) *principal {
	p, _ := r.Context().Value(ctxKey{}).(*principal)
	return p
}

const sessionCookie = "latch_cp_session"

// guard authenticates, blocks suspended accounts, and enforces CSRF/origin.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if isMutating(r.Method) {
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) {
				writeErr(w, 403, "cross-origin request refused")
				return
			}
		}
		// Open endpoints.
		if p == "/api/signup" || p == "/api/login" || strings.HasPrefix(p, "/ops/") {
			if strings.HasPrefix(p, "/ops/") && !s.opsAuthed(r) {
				writeErr(w, 401, "ops auth required")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		pr, err := s.authenticate(r)
		if err != nil {
			writeErr(w, 401, "authentication required")
			return
		}
		acct, err := s.st.GetAccount(r.Context(), pr.AccountID)
		if err != nil || acct.Status != store.AccountActive {
			writeErr(w, 403, "account is not active")
			return
		}
		if isMutating(r.Method) && !pr.ViaAPIKey {
			if subtle.ConstantTimeCompare([]byte(pr.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				writeErr(w, 403, "missing or invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, pr)))
	})
}

func (s *Server) authenticate(r *http.Request) (*principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		key, err := s.st.GetAPIKeyByHash(r.Context(), auth.HashAPIKey(strings.TrimPrefix(h, "Bearer ")))
		if err != nil {
			return nil, err
		}
		return &principal{AccountID: key.AccountID, ViaAPIKey: true}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, err
	}
	sess, err := s.st.GetSession(r.Context(), c.Value)
	if err != nil {
		return nil, err
	}
	return &principal{AccountID: sess.AccountID, UserID: sess.UserID, CSRF: sess.CSRF}, nil
}

func (s *Server) opsAuthed(r *http.Request) bool {
	if s.opts.OpsToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte("Bearer "+s.opts.OpsToken), []byte(r.Header.Get("Authorization"))) == 1
}

// --- handlers: auth ---

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, AccountName string }
	if !decode(w, r, &in) {
		return
	}
	if !strings.Contains(in.Email, "@") || len(in.Password) < 8 {
		writeErr(w, 400, "a valid email and an 8+ character password are required")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	name := in.AccountName
	if name == "" {
		name = in.Email
	}
	acct := store.Account{ID: id("acc"), Name: name, Status: store.AccountActive, PlanID: "free",
		Region: s.opts.Region, CreatedAt: time.Now().UTC()}
	owner := store.User{ID: id("usr"), Email: strings.ToLower(in.Email), PasswordHash: hash,
		Role: store.RoleOwner, CreatedAt: time.Now().UTC()}
	acct, owner, err = s.st.CreateAccountWithOwner(r.Context(), acct, owner)
	if err == store.ErrConflict {
		writeErr(w, 409, "email already registered")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.audit0(r, acct.ID, owner.ID, "user", "signup", "account", acct.ID)
	s.startSession(w, r, owner)
	writeJSON(w, 201, map[string]any{"account": acct})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, TOTP string }
	if !decode(w, r, &in) {
		return
	}
	u, err := s.st.GetUserByEmail(r.Context(), in.Email)
	if err != nil || !auth.VerifyPassword(u.PasswordHash, in.Password) {
		writeErr(w, 401, "invalid email or password")
		return
	}
	acct, err := s.st.GetAccount(r.Context(), u.AccountID)
	if err != nil || acct.Status == store.AccountSuspended {
		writeErr(w, 403, "account is suspended")
		return
	}
	if u.TOTPEnabledAt != nil {
		if in.TOTP == "" {
			writeJSON(w, 200, map[string]any{"mfaRequired": true})
			return
		}
		if !auth.ValidateTOTP(u.TOTPSecret, in.TOTP) {
			writeErr(w, 401, "invalid authentication code")
			return
		}
	}
	now := time.Now().UTC()
	u.LastLoginAt = &now
	_ = s.st.UpdateUser(r.Context(), u)
	s.audit0(r, u.AccountID, u.ID, "user", "login", "user", u.ID)
	s.startSession(w, r, u)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) {
	sess := store.Session{ID: auth.Token(), UserID: u.ID, AccountID: u.AccountID, CSRF: auth.Token(),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(24 * time.Hour)}
	_ = s.st.CreateSession(r.Context(), sess)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess.ID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	w.Header().Set("X-CSRF-Token", sess.CSRF)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	acct, _ := s.st.GetAccount(r.Context(), pr.AccountID)
	var user store.User
	if pr.UserID != "" {
		user, _ = s.st.GetUser(r.Context(), pr.UserID)
	}
	writeJSON(w, 200, map[string]any{"account": acct, "user": user, "csrfToken": pr.CSRF})
}

func (s *Server) mfaSetup(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	u, _ := s.st.GetUser(r.Context(), pr.UserID)
	secret, url, err := auth.NewTOTPSecret(s.opts.Issuer, u.Email)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	u.TOTPSecret = secret
	_ = s.st.UpdateUser(r.Context(), u)
	writeJSON(w, 200, map[string]any{"secret": secret, "otpauthUrl": url})
}

func (s *Server) mfaEnable(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct{ Code string }
	if !decode(w, r, &in) {
		return
	}
	u, _ := s.st.GetUser(r.Context(), pr.UserID)
	if u.TOTPSecret == "" || !auth.ValidateTOTP(u.TOTPSecret, in.Code) {
		writeErr(w, 400, "invalid code")
		return
	}
	now := time.Now().UTC()
	u.TOTPEnabledAt = &now
	_ = s.st.UpdateUser(r.Context(), u)
	s.audit0(r, u.AccountID, u.ID, "user", "mfa_enabled", "user", u.ID)
	writeJSON(w, 200, map[string]any{"enabled": true})
}

// --- helpers ---

func id(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func accountSlug(accountID string) string {
	s := strings.TrimPrefix(accountID, "acc_")
	if len(s) > 10 {
		s = s[:10]
	}
	return s
}

func (s *Server) audit0(r *http.Request, accountID, userID, actor, action, targetType, targetID string) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	_ = s.st.AppendAudit(r.Context(), store.AuditEntry{ID: id("aud"), AccountID: accountID, ActorUser: userID,
		Actor: actor, Action: action, TargetType: targetType, TargetID: targetID, IP: ip, CreatedAt: time.Now().UTC()})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func sameOrigin(origin, host string) bool {
	i := strings.Index(origin, "://")
	if i < 0 {
		return false
	}
	oh := origin[i+3:]
	h := oh
	if idx := strings.IndexByte(oh, ':'); idx >= 0 && !strings.Contains(oh, "]") {
		h = oh[:idx]
	}
	return oh == host || h == "localhost" || h == "127.0.0.1"
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
