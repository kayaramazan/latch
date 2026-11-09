package app

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/auth"
	"github.com/kayaramazan/latch/latch-control/internal/store"
)

// Anonymous "try without signing up" accounts. A guest gets a short-lived Free
// account and session so the dashboard works immediately; its tunnels are
// revoked when the trial passes. Anonymous public tunnels are an abuse vector,
// so the trial is deliberately small: short expiry, Free-plan hard caps, and an
// in-memory per-IP rate limit. Stronger controls (captcha, verified email
// before public exposure) are tracked for launch — see NOTES/SECURITY.
const (
	trialDuration       = time.Hour
	trialMaxPerIPPerDay = 3
)

// trialGate rate-limits trial creation per client IP (in-memory, best-effort;
// a multi-instance deployment should move this to the store/Redis).
type trialGate struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newTrialGate() *trialGate { return &trialGate{hits: map[string][]time.Time{}} }

// allow records an attempt from ip and reports whether it is within the limit.
func (g *trialGate) allow(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	cutoff := time.Now().Add(-24 * time.Hour)
	kept := g.hits[ip][:0]
	for _, t := range g.hits[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= trialMaxPerIPPerDay {
		g.hits[ip] = kept
		return false
	}
	g.hits[ip] = append(kept, time.Now())
	return true
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// startTrial creates an anonymous guest account + session (no email/password).
func (s *Server) startTrial(w http.ResponseWriter, r *http.Request) {
	if !s.trial.allow(clientIP(r)) {
		writeErr(w, 429, "too many trials from your network; please sign up to continue")
		return
	}
	now := time.Now().UTC()
	expires := now.Add(trialDuration)
	acct := store.Account{ID: id("acc"), Name: "Guest trial", Status: store.AccountActive, PlanID: "free",
		Region: s.opts.Region, CreatedAt: now, TrialExpiresAt: &expires}
	// A random, unusable credential: the guest cannot log in until they claim the
	// account (which sets a real email + password).
	guestEmail := "guest-" + strings.TrimPrefix(acct.ID, "acc_") + "@trial.invalid"
	hash, err := auth.HashPassword(auth.Token())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	owner := store.User{ID: id("usr"), Email: guestEmail, PasswordHash: hash, Role: store.RoleOwner, CreatedAt: now}
	acct, owner, err = s.st.CreateAccountWithOwner(r.Context(), acct, owner)
	if err != nil {
		writeErr(w, 500, "could not start trial")
		return
	}
	s.audit0(r, acct.ID, owner.ID, "user", "trial_started", "account", acct.ID)
	s.startSession(w, r, owner)
	writeJSON(w, 201, map[string]any{"account": acct, "trialExpiresAt": expires})
}

// claimTrial converts the current guest account into a normal one by setting a
// real email and password, and clears the trial expiry so it is never reaped.
func (s *Server) claimTrial(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct{ Email, Password, AccountName string }
	if !decode(w, r, &in) {
		return
	}
	if !strings.Contains(in.Email, "@") || len(in.Password) < 8 {
		writeErr(w, 400, "a valid email and an 8+ character password are required")
		return
	}
	acct, err := s.st.GetAccount(r.Context(), pr.AccountID)
	if err != nil {
		writeErr(w, 404, "account not found")
		return
	}
	if acct.TrialExpiresAt == nil {
		writeErr(w, 409, "this account is already registered")
		return
	}
	if _, err := s.st.GetUserByEmail(r.Context(), in.Email); err == nil {
		writeErr(w, 409, "email already registered")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if err := s.st.SetUserCredentials(r.Context(), pr.UserID, in.Email, hash); err == store.ErrConflict {
		writeErr(w, 409, "email already registered")
		return
	} else if err != nil {
		writeErr(w, 500, "could not save credentials")
		return
	}
	_ = s.st.SetAccountTrial(r.Context(), acct.ID, nil)
	s.audit0(r, acct.ID, pr.UserID, "user", "trial_claimed", "account", acct.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// reapTrials suspends guest accounts whose trial has passed (which revokes their
// tunnels on the fleet via the existing suspend flow).
func (s *Server) reapTrials(ctx context.Context) {
	accts, err := s.st.ListAllAccounts(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, a := range accts {
		if a.TrialExpiresAt != nil && a.Status == store.AccountActive && now.After(*a.TrialExpiresAt) {
			s.suspend(ctx, a.ID, "trial expired")
			_ = s.st.SetAccountTrial(ctx, a.ID, nil)
		}
	}
}

// trialLoop runs reapTrials until ctx is done.
func (s *Server) trialLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.reapTrials(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
