package acme

import (
	"context"
	"fmt"
	"strings"

	"github.com/mholt/acmez/v3/acme"
)

// ManualRecord is the TXT record the operator must create for a DNS-01
// challenge in manual mode.
type ManualRecord struct {
	Name  string // e.g. _acme-challenge.relay.example.com
	Value string // the TXT value
}

// ManualHooks drive the manual DNS-01 mode. Present shows the record and
// blocks until the operator confirms it is live (or ctx is cancelled); CleanUp
// is optional. The wizard supplies interactive hooks; the daemon supplies
// hooks that log and wait.
type ManualHooks struct {
	BaseDomain string
	Present    func(ctx context.Context, rec ManualRecord) error
	CleanUp    func(ctx context.Context, rec ManualRecord) error
}

// manualSolver implements acmez.Solver for manual DNS-01. It only handles
// names under the base domain; for any other name it returns an error so
// certmagic falls through to the next issuer (TLS-ALPN for custom domains).
type manualSolver struct {
	hooks ManualHooks
}

func (s *manualSolver) rec(chal acme.Challenge) ManualRecord {
	return ManualRecord{Name: chal.DNS01TXTRecordName(), Value: chal.DNS01KeyAuthorization()}
}

// domainOf extracts the validated domain from the TXT record name by stripping
// the _acme-challenge prefix.
func domainOf(recordName string) string {
	return normalizeName(strings.TrimPrefix(recordName, "_acme-challenge."))
}

func (s *manualSolver) Present(ctx context.Context, chal acme.Challenge) error {
	rec := s.rec(chal)
	if !underBase(domainOf(rec.Name), s.hooks.BaseDomain) {
		return fmt.Errorf("manual solver only handles the base domain, not %q", domainOf(rec.Name))
	}
	if s.hooks.Present == nil {
		return fmt.Errorf("manual DNS mode needs a TXT record but no handler is configured: %s = %s", rec.Name, rec.Value)
	}
	return s.hooks.Present(ctx, rec)
}

// Wait is a no-op: Present already blocks until the record is live.
func (s *manualSolver) Wait(ctx context.Context, chal acme.Challenge) error { return nil }

func (s *manualSolver) CleanUp(ctx context.Context, chal acme.Challenge) error {
	if s.hooks.CleanUp == nil {
		return nil
	}
	return s.hooks.CleanUp(ctx, s.rec(chal))
}
