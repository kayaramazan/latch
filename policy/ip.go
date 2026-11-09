package policy

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/kayaramazan/latch/proto/model"
)

type compiledRule struct {
	action  model.Action
	prefix  netip.Prefix
	country string // uppercase ISO code; set => country rule
}

type compiledIPPolicy struct {
	id            string
	defaultAction model.Action
	onDeny        model.DenyBehavior
	rules         []compiledRule
	global        bool
	hasGeoRule    bool
}

func compileIPPolicy(p model.IPPolicy) (*compiledIPPolicy, error) {
	p.Normalize()
	c := &compiledIPPolicy{id: p.ID, defaultAction: p.DefaultAction, onDeny: p.OnDeny, global: p.Global}
	for i, r := range p.Rules {
		switch {
		case r.Country != "":
			c.rules = append(c.rules, compiledRule{action: r.Action, country: strings.ToUpper(r.Country)})
			c.hasGeoRule = true
		case r.CIDR != "":
			pfx, err := netip.ParsePrefix(r.CIDR)
			if err != nil {
				return nil, fmt.Errorf("policy %s rule %d: bad cidr %q: %w", p.ID, i, r.CIDR, err)
			}
			c.rules = append(c.rules, compiledRule{action: r.Action, prefix: pfx})
		default:
			return nil, fmt.Errorf("policy %s rule %d: neither cidr nor country set", p.ID, i)
		}
	}
	return c, nil
}

// ipMatch is the result of matching one policy against a request.
type ipMatch struct {
	action model.Action
	isGeo  bool
}

// match returns the first rule that matches addr/country, or nil.
func (c *compiledIPPolicy) match(addr netip.Addr, country string) *ipMatch {
	addr = addr.Unmap()
	for _, r := range c.rules {
		if r.country != "" {
			if country != "" && strings.EqualFold(country, r.country) {
				return &ipMatch{action: r.action, isGeo: true}
			}
			continue
		}
		if r.prefix.Contains(addr) {
			return &ipMatch{action: r.action, isGeo: false}
		}
	}
	return nil
}

// ipResult is the combined IP/Geo decision.
type ipResult struct {
	deny   bool
	policy *compiledIPPolicy
	isGeo  bool
}

// combineIP applies the precedence documented in ROADMAP phase 5:
// global deny → route deny → route allow → global allow →
// route default → global default → allow.
func combineIP(globals []*compiledIPPolicy, route *compiledIPPolicy, addr netip.Addr, country string) ipResult {
	var routeMatch *ipMatch
	if route != nil {
		routeMatch = route.match(addr, country)
	}
	// 1. global explicit deny
	for _, g := range globals {
		if m := g.match(addr, country); m != nil && m.action == model.ActionDeny {
			return ipResult{deny: true, policy: g, isGeo: m.isGeo}
		}
	}
	// 2. route explicit deny
	if routeMatch != nil && routeMatch.action == model.ActionDeny {
		return ipResult{deny: true, policy: route, isGeo: routeMatch.isGeo}
	}
	// 3. route explicit allow
	if routeMatch != nil && routeMatch.action == model.ActionAllow {
		return ipResult{}
	}
	// 4. global explicit allow
	for _, g := range globals {
		if m := g.match(addr, country); m != nil && m.action == model.ActionAllow {
			return ipResult{}
		}
	}
	// 5. defaults: route first, then globals
	if route != nil && route.defaultAction == model.ActionDeny {
		return ipResult{deny: true, policy: route}
	}
	for _, g := range globals {
		if g.defaultAction == model.ActionDeny {
			return ipResult{deny: true, policy: g}
		}
	}
	return ipResult{}
}
