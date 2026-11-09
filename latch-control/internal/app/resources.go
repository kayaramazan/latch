package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/auth"
	"github.com/kayaramazan/latch/latch-control/internal/plan"
	"github.com/kayaramazan/latch/latch-control/internal/store"
	"github.com/kayaramazan/latch/proto/control"
)

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, _ := s.st.ListProjects(r.Context(), s.principalOf(r).AccountID)
	writeJSON(w, 200, ps)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	p := store.Project{ID: id("prj"), AccountID: s.principalOf(r).AccountID, Name: in.Name, CreatedAt: time.Now().UTC()}
	_ = s.st.CreateProject(r.Context(), p)
	writeJSON(w, 201, p)
}

// --- tokens (self-service, placed on the fleet) ---

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, _ := s.st.ListTokens(r.Context(), s.principalOf(r).AccountID)
	writeJSON(w, 200, ts)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct {
		Name      string `json:"name"`
		ProjectID string `json:"projectId"`
		TCP       bool   `json:"tcp"`
	}
	if !decode(w, r, &in) {
		return
	}
	acct, _ := s.st.GetAccount(r.Context(), pr.AccountID)
	pl := plan.Get(acct.PlanID)
	existing, _ := s.st.ListTokens(r.Context(), pr.AccountID)
	if activeCount(existing) >= pl.MaxTokens {
		writeErr(w, 402, "token limit reached for your plan; upgrade to add more")
		return
	}
	relayID, ok := s.fleet.PickRelay(acct.Region)
	if !ok {
		writeErr(w, 503, "no relay available in your region yet")
		return
	}
	client, baseDomain, ok := s.fleet.RelayFor(relayID)
	if !ok {
		writeErr(w, 503, "relay unavailable")
		return
	}
	// Each account gets an account-prefixed hostname under the regional base
	// domain. The prefix ties the name to the tenant; the relay registry
	// prevents another tenant from claiming it. A single wildcard cert
	// (*.baseDomain) covers it (no per-subdomain ACME).
	host := accountSlug(pr.AccountID) + "-" + slugify(in.Name) + "." + baseDomain
	scope := []string{host}
	res, err := client.CreateToken(r.Context(), control.CreateTokenRequest{Name: in.Name, Hostnames: scope, TCP: in.TCP})
	if err != nil {
		writeErr(w, 502, "could not create token on the relay: "+err.Error())
		return
	}
	tok := store.TunnelToken{ID: res.Token.ID, AccountID: pr.AccountID, ProjectID: in.ProjectID, RelayID: relayID,
		Name: in.Name, Hostnames: scope, TCP: in.TCP, Status: "active", CreatedAt: time.Now().UTC()}
	_ = s.st.CreateToken(r.Context(), tok)
	s.audit0(r, pr.AccountID, pr.UserID, "user", "token_created", "token", tok.ID)
	writeJSON(w, 201, map[string]any{"token": tok, "connectionString": res.ConnectionString, "hostname": host})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	tok, err := s.st.GetToken(r.Context(), pr.AccountID, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "token not found")
		return
	}
	if client, _, ok := s.fleet.RelayFor(tok.RelayID); ok {
		_ = client.RevokeToken(r.Context(), tok.ID)
	}
	_ = s.st.SetTokenStatus(r.Context(), pr.AccountID, tok.ID, "revoked")
	s.audit0(r, pr.AccountID, pr.UserID, "user", "token_revoked", "token", tok.ID)
	w.WriteHeader(204)
}

// slugify makes a DNS-safe label from a name.
func slugify(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else if c == ' ' || c == '-' || c == '_' {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// --- custom domains ---

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	ds, _ := s.st.ListDomains(r.Context(), s.principalOf(r).AccountID)
	writeJSON(w, 200, ds)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct{ Domain string }
	if !decode(w, r, &in) {
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	if in.Domain == "" {
		writeErr(w, 400, "domain is required")
		return
	}
	if blocked, _ := s.st.IsBlocked(r.Context(), in.Domain); blocked {
		writeErr(w, 403, "this domain is not allowed")
		return
	}
	acct, _ := s.st.GetAccount(r.Context(), pr.AccountID)
	pl := plan.Get(acct.PlanID)
	if pl.MaxDomains <= 0 {
		writeErr(w, 402, "custom domains are not available on your plan; upgrade to add one")
		return
	}
	existing, _ := s.st.ListDomains(r.Context(), pr.AccountID)
	if len(existing) >= pl.MaxDomains {
		writeErr(w, 402, "custom domain limit reached for your plan")
		return
	}
	relayID, ok := s.fleet.PickRelay(acct.Region)
	if !ok {
		writeErr(w, 503, "no relay available")
		return
	}
	client, _, _ := s.fleet.RelayFor(relayID)
	if _, err := client.CreateDomain(r.Context(), control.CreateDomainRequest{Domain: in.Domain}); err != nil {
		writeErr(w, 502, "could not register domain on the relay: "+err.Error())
		return
	}
	d := store.CustomDomain{ID: id("dom"), AccountID: pr.AccountID, Domain: in.Domain, RelayID: relayID,
		Status: "pending", CreatedAt: time.Now().UTC()}
	_ = s.st.CreateDomain(r.Context(), d)
	s.audit0(r, pr.AccountID, pr.UserID, "user", "domain_created", "domain", d.ID)
	writeJSON(w, 201, d)
}

func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	d, err := s.st.GetDomain(r.Context(), pr.AccountID, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "domain not found")
		return
	}
	client, _, ok := s.fleet.RelayFor(d.RelayID)
	if !ok {
		writeErr(w, 503, "relay unavailable")
		return
	}
	res, err := client.VerifyDomain(r.Context(), d.Domain)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if res.Verified {
		_ = s.st.SetDomainVerified(r.Context(), pr.AccountID, d.ID, time.Now().UTC())
	}
	writeJSON(w, 200, res)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	d, err := s.st.GetDomain(r.Context(), pr.AccountID, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "domain not found")
		return
	}
	if client, _, ok := s.fleet.RelayFor(d.RelayID); ok {
		_ = client.DeleteDomain(r.Context(), d.Domain)
	}
	_ = s.st.DeleteDomain(r.Context(), pr.AccountID, d.ID)
	w.WriteHeader(204)
}

// activeCount returns the number of non-revoked tokens.
func activeCount(ts []store.TunnelToken) int {
	n := 0
	for _, t := range ts {
		if t.Status == "active" {
			n++
		}
	}
	return n
}

// usage reports the account's current-month usage against its plan.
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	acct, _ := s.st.GetAccount(r.Context(), pr.AccountID)
	pl := plan.Get(acct.PlanID)
	monthStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	bin, bout, reqs, _ := s.st.MonthUsage(r.Context(), pr.AccountID, monthStart)
	toks, _ := s.st.ListTokens(r.Context(), pr.AccountID)
	doms, _ := s.st.ListDomains(r.Context(), pr.AccountID)
	writeJSON(w, 200, map[string]any{
		"plan": pl, "bytesIn": bin, "bytesOut": bout, "bytesTotal": bin + bout,
		"bandwidthLimit": pl.BandwidthBytes(), "requests": reqs,
		"tokens": activeCount(toks), "tokenLimit": pl.MaxTokens,
		"domains": len(doms), "domainLimit": pl.MaxDomains,
	})
}

// --- api keys ---

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	ks, _ := s.st.ListAPIKeys(r.Context(), s.principalOf(r).AccountID)
	writeJSON(w, 200, ks)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	plain, prefix, hash := auth.APIKey()
	k := store.APIKey{ID: id("key"), AccountID: pr.AccountID, Name: in.Name, Hash: hash, Prefix: prefix, CreatedAt: time.Now().UTC()}
	_ = s.st.CreateAPIKey(r.Context(), k)
	s.audit0(r, pr.AccountID, pr.UserID, "user", "apikey_created", "apikey", k.ID)
	writeJSON(w, 201, map[string]any{"key": k, "secret": plain})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	if err := s.st.RevokeAPIKey(r.Context(), pr.AccountID, r.PathValue("id")); err != nil {
		writeErr(w, 404, "key not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	es, _ := s.st.ListAudit(r.Context(), s.principalOf(r).AccountID, 100)
	writeJSON(w, 200, es)
}

// --- ops (operator, bearer-guarded) ---

func (s *Server) opsSuspend(w http.ResponseWriter, r *http.Request) {
	var in struct{ Reason string }
	_ = decode(w, r, &in)
	s.suspend(r.Context(), r.PathValue("id"), in.Reason)
	w.WriteHeader(204)
}

func (s *Server) opsUnsuspend(w http.ResponseWriter, r *http.Request) {
	_ = s.st.SetAccountStatus(r.Context(), r.PathValue("id"), store.AccountActive, "")
	w.WriteHeader(204)
}

func (s *Server) opsBlocklist(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pattern, Reason string }
	if !decode(w, r, &in) {
		return
	}
	_ = s.st.AddBlocklist(r.Context(), in.Pattern, in.Reason)
	w.WriteHeader(204)
}

// suspend marks the account suspended and revokes all its tokens on the fleet,
// so suspended tenants stop serving traffic.
func (s *Server) suspend(ctx context.Context, accountID, reason string) {
	_ = s.st.SetAccountStatus(ctx, accountID, store.AccountSuspended, reason)
	toks, _ := s.st.TokensForAccount(ctx, accountID)
	for _, t := range toks {
		if client, _, ok := s.fleet.RelayFor(t.RelayID); ok {
			_ = client.RevokeToken(ctx, t.ID)
		}
		_ = s.st.SetTokenStatus(ctx, accountID, t.ID, "revoked")
	}
	_ = s.st.AppendAudit(ctx, store.AuditEntry{ID: id("aud"), AccountID: accountID, Actor: "system",
		Action: "account_suspended", TargetType: "account", TargetID: accountID, Metadata: reason, CreatedAt: time.Now().UTC()})
}

// Suspend exposes the suspend flow (for tests / abuse hooks).
func (s *Server) Suspend(ctx context.Context, accountID, reason string) {
	s.suspend(ctx, accountID, reason)
}
