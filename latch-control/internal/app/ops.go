package app

import (
	"net/http"

	"github.com/kayaramazan/latch/latch-control/internal/plan"
	"github.com/kayaramazan/latch/latch-control/internal/store"
	"time"
)

// opsAccounts lists accounts with plan + current-month usage (operator view).
func (s *Server) opsAccounts(w http.ResponseWriter, r *http.Request) {
	accts, err := s.st.ListAllAccounts(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	monthStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]map[string]any, 0, len(accts))
	for _, a := range accts {
		bin, bout, _, _ := s.st.MonthUsage(r.Context(), a.ID, monthStart)
		out = append(out, map[string]any{"account": a, "plan": plan.Get(a.PlanID).Name, "bytesTotal": bin + bout})
	}
	writeJSON(w, 200, out)
}

// opsRevenue sums monthly recurring revenue from active plans (cents).
func (s *Server) opsRevenue(w http.ResponseWriter, r *http.Request) {
	accts, _ := s.st.ListAllAccounts(r.Context())
	var mrr int
	counts := map[string]int{}
	for _, a := range accts {
		if a.Status != store.AccountActive {
			continue
		}
		p := plan.Get(a.PlanID)
		mrr += p.PriceCents
		counts[p.Name]++
	}
	writeJSON(w, 200, map[string]any{"mrrCents": mrr, "accountsByPlan": counts})
}

// exportAccount returns a GDPR/KVKK data dump for the caller's account.
func (s *Server) exportAccount(w http.ResponseWriter, r *http.Request) {
	data, err := s.st.ExportAccount(r.Context(), s.principalOf(r).AccountID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=latch-account.json")
	writeJSON(w, 200, data)
}

// deleteAccount closes and purges the caller's account (right to erasure).
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	// Revoke tokens on the fleet first.
	toks, _ := s.st.TokensForAccount(r.Context(), pr.AccountID)
	for _, t := range toks {
		if client, _, ok := s.fleet.RelayFor(t.RelayID); ok {
			_ = client.RevokeToken(r.Context(), t.ID)
		}
	}
	if err := s.st.DeleteAccount(r.Context(), pr.AccountID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	w.WriteHeader(204)
}
