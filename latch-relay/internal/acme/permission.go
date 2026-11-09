// Package acme manages the relay's TLS certificates with certmagic: a wildcard
// certificate for the base domain (ACME DNS-01) and on-demand certificates for
// registered custom domains (ACME TLS-ALPN-01). The permission function here
// is the abuse guard: a custom domain gets a certificate only if the operator
// registered it AND its DNS points at this relay.
package acme

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Resolver abstracts the DNS lookups the permission check needs; production
// uses net.DefaultResolver, tests inject a fake.
type Resolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DomainChecker reports whether a custom domain has been registered by the
// relay operator (implemented by the custom-domain store).
type DomainChecker interface {
	IsRegistered(domain string) bool
}

// normalizeName lowercases a DNS name and strips a trailing dot.
func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// underBase reports whether name is the base domain or a subdomain of it.
func underBase(name, base string) bool {
	name, base = normalizeName(name), normalizeName(base)
	return name == base || strings.HasSuffix(name, "."+base)
}

// decide is the certmagic OnDemand DecisionFunc. It allows:
//   - any name under the base domain (served by the wildcard certificate), and
//   - a custom domain only when it is registered and its DNS points here.
//
// Everything else is denied, so an attacker cannot make the relay request
// certificates for arbitrary names (ACME rate-limit / abuse protection).
func (m *Manager) decide(ctx context.Context, name string) error {
	name = normalizeName(name)
	if name == "" {
		return errors.New("empty server name")
	}
	if underBase(name, m.baseDomain) {
		return nil
	}
	if m.domains == nil || !m.domains.IsRegistered(name) {
		return fmt.Errorf("custom domain %q is not registered on this relay", name)
	}
	if err := pointsHere(ctx, m.resolver, name, m.publicHost, m.publicIP); err != nil {
		return fmt.Errorf("custom domain %q does not point to this relay: %w", name, err)
	}
	return nil
}

// pointsHere verifies that name is a CNAME to publicHost or has an A/AAAA
// record equal to publicIP. Either is sufficient; this is what stops the relay
// from obtaining a certificate for a domain that has not delegated to it.
func pointsHere(ctx context.Context, r Resolver, name, publicHost, publicIP string) error {
	publicHost = normalizeName(publicHost)
	if cname, err := r.LookupCNAME(ctx, name); err == nil {
		if normalizeName(cname) == publicHost {
			return nil
		}
	}
	if publicIP != "" {
		if addrs, err := r.LookupHost(ctx, name); err == nil {
			for _, a := range addrs {
				if a == publicIP {
					return nil
				}
			}
		}
	}
	msg := "expected a CNAME to " + publicHost
	if publicIP != "" {
		msg += " or an A record to " + publicIP
	}
	return errors.New(msg)
}

// systemResolver adapts net.Resolver to the Resolver interface.
type systemResolver struct{ r *net.Resolver }

func (s systemResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return s.r.LookupCNAME(ctx, host)
}

func (s systemResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return s.r.LookupHost(ctx, host)
}

// DefaultResolver returns a Resolver backed by net.DefaultResolver.
func DefaultResolver() Resolver { return systemResolver{r: net.DefaultResolver} }

// CheckPointsHere verifies that name is a CNAME to publicHost or has an A/AAAA
// record equal to publicIP. It is used by `latch-relay domain verify` and the
// install wizard.
func CheckPointsHere(ctx context.Context, r Resolver, name, publicHost, publicIP string) error {
	return pointsHere(ctx, r, name, publicHost, publicIP)
}
