package acme

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	cname map[string]string
	hosts map[string][]string
}

func (f fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := f.cname[normalizeName(host)]; ok {
		return c, nil
	}
	return host, nil // net.Resolver returns the host itself when there is no CNAME
}

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f.hosts[normalizeName(host)]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

type fakeDomains map[string]bool

func (d fakeDomains) IsRegistered(domain string) bool { return d[normalizeName(domain)] }

func newManager(res Resolver, domains DomainChecker) *Manager {
	return &Manager{
		baseDomain: "relay.example.com",
		publicHost: "relay.example.com",
		publicIP:   "203.0.113.10",
		domains:    domains,
		resolver:   res,
	}
}

func TestDecideBaseDomain(t *testing.T) {
	m := newManager(fakeResolver{}, fakeDomains{})
	for _, name := range []string{"relay.example.com", "x.relay.example.com", "a.b.relay.example.com", "RELAY.example.com", "x.relay.example.com."} {
		if err := m.decide(context.Background(), name); err != nil {
			t.Errorf("%q should be allowed (wildcard), got %v", name, err)
		}
	}
}

func TestDecideCustomDomainRegisteredWithCNAME(t *testing.T) {
	res := fakeResolver{cname: map[string]string{"app.customer.com": "relay.example.com."}}
	m := newManager(res, fakeDomains{"app.customer.com": true})
	if err := m.decide(context.Background(), "app.customer.com"); err != nil {
		t.Errorf("registered custom domain with correct CNAME should be allowed, got %v", err)
	}
}

func TestDecideCustomDomainRegisteredWithARecord(t *testing.T) {
	res := fakeResolver{hosts: map[string][]string{"app.customer.com": {"203.0.113.10"}}}
	m := newManager(res, fakeDomains{"app.customer.com": true})
	if err := m.decide(context.Background(), "app.customer.com"); err != nil {
		t.Errorf("registered custom domain with correct A record should be allowed, got %v", err)
	}
}

func TestDecideCustomDomainNotRegistered(t *testing.T) {
	res := fakeResolver{cname: map[string]string{"evil.com": "relay.example.com."}}
	m := newManager(res, fakeDomains{})
	if err := m.decide(context.Background(), "evil.com"); err == nil {
		t.Error("unregistered custom domain must be denied even with a correct CNAME")
	}
}

func TestDecideCustomDomainWrongDNS(t *testing.T) {
	res := fakeResolver{
		cname: map[string]string{"app.customer.com": "someone-else.net."},
		hosts: map[string][]string{"app.customer.com": {"198.51.100.5"}},
	}
	m := newManager(res, fakeDomains{"app.customer.com": true})
	if err := m.decide(context.Background(), "app.customer.com"); err == nil {
		t.Error("registered custom domain pointing elsewhere must be denied")
	}
}

func TestDecideNilDomains(t *testing.T) {
	m := newManager(fakeResolver{}, nil)
	if err := m.decide(context.Background(), "app.customer.com"); err == nil {
		t.Error("custom domain must be denied when no domain registry is configured")
	}
}

func TestDecideEmptyName(t *testing.T) {
	m := newManager(fakeResolver{}, fakeDomains{})
	if err := m.decide(context.Background(), ""); err == nil {
		t.Error("empty name must be denied")
	}
}

func TestUnderBase(t *testing.T) {
	cases := []struct {
		name, base string
		want       bool
	}{
		{"relay.example.com", "relay.example.com", true},
		{"x.relay.example.com", "relay.example.com", true},
		{"notrelay.example.com", "relay.example.com", false},
		{"relay.example.com.evil.com", "relay.example.com", false},
		{"example.com", "relay.example.com", false},
	}
	for _, c := range cases {
		if got := underBase(c.name, c.base); got != c.want {
			t.Errorf("underBase(%q,%q)=%v want %v", c.name, c.base, got, c.want)
		}
	}
}
