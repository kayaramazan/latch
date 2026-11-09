package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/kayaramazan/latch/latch-control/internal/plan"
	"github.com/kayaramazan/latch/latch-control/internal/store"
)

// morEvent is a provider-agnostic billing webhook event. The Merchant-of-Record
// (Paddle/LemonSqueezy) adapter maps its payload to this shape; `accountId` is
// passed through at checkout.
type morEvent struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"` // subscription.active | payment.failed | subscription.canceled
	AccountID      string    `json:"accountId"`
	SubscriptionID string    `json:"subscriptionId"`
	PlanID         string    `json:"planId"`
	PeriodEnd      time.Time `json:"periodEnd"`
}

// morWebhook ingests billing webhooks. It verifies the HMAC signature, is
// idempotent, and maps events to plan/subscription/suspension changes.
func (s *Server) morWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if s.opts.WebhookSecret != "" && !validSignature(s.opts.WebhookSecret, body, r.Header.Get("X-Mor-Signature")) {
		writeErr(w, 401, "invalid signature")
		return
	}
	var ev morEvent
	if err := jsonUnmarshal(body, &ev); err != nil || ev.ID == "" || ev.AccountID == "" {
		writeErr(w, 400, "bad event")
		return
	}
	first, err := s.st.RecordBillingEvent(r.Context(), ev.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !first {
		w.WriteHeader(200) // already processed
		return
	}
	ctx := r.Context()
	switch ev.Type {
	case "subscription.active", "subscription.updated":
		pl := plan.Get(ev.PlanID)
		_ = s.st.SetAccountPlan(ctx, ev.AccountID, pl.ID)
		_ = s.st.SetSubscription(ctx, store.Subscription{AccountID: ev.AccountID, MORSubID: ev.SubscriptionID,
			PlanID: pl.ID, Status: "active", CurrentPeriodEnd: ev.PeriodEnd})
		// Resume if previously suspended for billing.
		if a, err := s.st.GetAccount(ctx, ev.AccountID); err == nil && a.Status == store.AccountSuspended {
			_ = s.st.SetAccountStatus(ctx, ev.AccountID, store.AccountActive, "")
		}
		s.auditSys(ctx, ev.AccountID, "subscription_active", ev.PlanID)
	case "payment.failed", "subscription.past_due":
		_ = s.st.SetSubscription(ctx, store.Subscription{AccountID: ev.AccountID, MORSubID: ev.SubscriptionID,
			PlanID: ev.PlanID, Status: "past_due", CurrentPeriodEnd: ev.PeriodEnd})
		s.suspend(ctx, ev.AccountID, "payment failed")
	case "subscription.canceled":
		_ = s.st.SetAccountPlan(ctx, ev.AccountID, "free")
		_ = s.st.SetSubscription(ctx, store.Subscription{AccountID: ev.AccountID, MORSubID: ev.SubscriptionID,
			PlanID: "free", Status: "canceled", CurrentPeriodEnd: ev.PeriodEnd})
		s.auditSys(ctx, ev.AccountID, "subscription_canceled", "")
	}
	w.WriteHeader(200)
}

// checkout returns a (stubbed) MoR hosted-checkout URL for a plan. A real
// integration creates a checkout session with accountId as passthrough.
func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	pr := s.principalOf(r)
	var in struct{ PlanID string }
	if !decode(w, r, &in) {
		return
	}
	pl := plan.Get(in.PlanID)
	url := s.opts.CheckoutBaseURL + "?plan=" + pl.ID + "&account=" + pr.AccountID
	writeJSON(w, 200, map[string]any{"checkoutUrl": url, "plan": pl})
}

func validSignature(secret string, body []byte, sig string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

func (s *Server) auditSys(ctx context.Context, accountID, action, meta string) {
	_ = s.st.AppendAudit(ctx, store.AuditEntry{ID: id("aud"), AccountID: accountID, Actor: "mor",
		Action: action, TargetType: "account", TargetID: accountID, Metadata: meta, CreatedAt: time.Now().UTC()})
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
