package model

import (
	"reflect"
	"sort"
)

// ObjectKind names the kinds of top-level objects in a snapshot.
type ObjectKind string

const (
	KindRoute     ObjectKind = "route"
	KindIPPolicy  ObjectKind = "ipPolicy"
	KindRateLimit ObjectKind = "rateLimit"
	KindRelay     ObjectKind = "relay"
	KindSettings  ObjectKind = "settings"
)

// Ref identifies one object in a diff.
type Ref struct {
	Kind ObjectKind `json:"kind"`
	ID   string     `json:"id,omitempty"`
	Name string     `json:"name,omitempty"`
}

// Diff describes what changed between two snapshots and which routes are
// affected by it (see docs/DATA_MODEL.md).
type Diff struct {
	Added          []Ref    `json:"added"`
	Removed        []Ref    `json:"removed"`
	Changed        []Ref    `json:"changed"`
	AffectedRoutes []string `json:"affectedRoutes"`
}

// Empty reports whether nothing changed.
func (d *Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Summary returns a short human-readable description, e.g. "2 changed, 1 added".
func (d *Diff) Summary() string {
	if d.Empty() {
		return "no changes"
	}
	parts := []string{}
	add := func(n int, word string) {
		if n > 0 {
			parts = append(parts, itoa(n)+" "+word)
		}
	}
	add(len(d.Added), "added")
	add(len(d.Changed), "changed")
	add(len(d.Removed), "removed")
	s := parts[0]
	for _, p := range parts[1:] {
		s += ", " + p
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ComputeDiff compares old and new. A route is affected when it was added,
// removed or changed; when an IP policy, rate limit or relay it references
// changed or was removed; when any global policy/rate limit changed; or when
// settings that influence proxying changed.
func ComputeDiff(oldS, newS *Snapshot) Diff {
	d := Diff{Added: []Ref{}, Removed: []Ref{}, Changed: []Ref{}}
	affected := map[string]bool{}
	allRoutes := func() {
		for _, r := range oldS.Routes {
			affected[r.ID] = true
		}
		for _, r := range newS.Routes {
			affected[r.ID] = true
		}
	}

	changedPolicies := map[string]bool{}
	changedRates := map[string]bool{}
	changedRelays := map[string]bool{}
	globalChanged := false

	// Routes.
	oldRoutes := map[string]Route{}
	for _, r := range oldS.Routes {
		oldRoutes[r.ID] = r
	}
	newRoutes := map[string]Route{}
	for _, r := range newS.Routes {
		newRoutes[r.ID] = r
	}
	for id, nr := range newRoutes {
		or, ok := oldRoutes[id]
		switch {
		case !ok:
			d.Added = append(d.Added, Ref{KindRoute, id, nr.Name})
			affected[id] = true
		case !routeEqual(or, nr):
			d.Changed = append(d.Changed, Ref{KindRoute, id, nr.Name})
			affected[id] = true
		}
	}
	for id, or := range oldRoutes {
		if _, ok := newRoutes[id]; !ok {
			d.Removed = append(d.Removed, Ref{KindRoute, id, or.Name})
			affected[id] = true
		}
	}

	// IP policies.
	oldPol := map[string]IPPolicy{}
	for _, p := range oldS.IPPolicies {
		oldPol[p.ID] = p
	}
	newPol := map[string]IPPolicy{}
	for _, p := range newS.IPPolicies {
		newPol[p.ID] = p
	}
	for id, np := range newPol {
		op, ok := oldPol[id]
		switch {
		case !ok:
			d.Added = append(d.Added, Ref{KindIPPolicy, id, np.Name})
			changedPolicies[id] = true
			globalChanged = globalChanged || np.Global
		case !reflect.DeepEqual(op, np):
			d.Changed = append(d.Changed, Ref{KindIPPolicy, id, np.Name})
			changedPolicies[id] = true
			globalChanged = globalChanged || np.Global || op.Global
		}
	}
	for id, op := range oldPol {
		if _, ok := newPol[id]; !ok {
			d.Removed = append(d.Removed, Ref{KindIPPolicy, id, op.Name})
			changedPolicies[id] = true
			globalChanged = globalChanged || op.Global
		}
	}

	// Rate limits.
	oldRL := map[string]RateLimitRule{}
	for _, r := range oldS.RateLimits {
		oldRL[r.ID] = r
	}
	newRL := map[string]RateLimitRule{}
	for _, r := range newS.RateLimits {
		newRL[r.ID] = r
	}
	for id, nr := range newRL {
		or, ok := oldRL[id]
		switch {
		case !ok:
			d.Added = append(d.Added, Ref{KindRateLimit, id, nr.Name})
			changedRates[id] = true
			globalChanged = globalChanged || nr.Global
		case !reflect.DeepEqual(or, nr):
			d.Changed = append(d.Changed, Ref{KindRateLimit, id, nr.Name})
			changedRates[id] = true
			globalChanged = globalChanged || nr.Global || or.Global
		}
	}
	for id, or := range oldRL {
		if _, ok := newRL[id]; !ok {
			d.Removed = append(d.Removed, Ref{KindRateLimit, id, or.Name})
			changedRates[id] = true
			globalChanged = globalChanged || or.Global
		}
	}

	// Relays.
	oldRelay := map[string]Relay{}
	for _, r := range oldS.Relays {
		oldRelay[r.ID] = r
	}
	newRelay := map[string]Relay{}
	for _, r := range newS.Relays {
		newRelay[r.ID] = r
	}
	for id, nr := range newRelay {
		or, ok := oldRelay[id]
		switch {
		case !ok:
			d.Added = append(d.Added, Ref{KindRelay, id, nr.Name})
			changedRelays[id] = true
		case !reflect.DeepEqual(or, nr):
			d.Changed = append(d.Changed, Ref{KindRelay, id, nr.Name})
			changedRelays[id] = true
		}
	}
	for id, or := range oldRelay {
		if _, ok := newRelay[id]; !ok {
			d.Removed = append(d.Removed, Ref{KindRelay, id, or.Name})
			changedRelays[id] = true
		}
	}

	// Settings.
	if !reflect.DeepEqual(oldS.Settings, newS.Settings) {
		d.Changed = append(d.Changed, Ref{Kind: KindSettings})
		if settingsAffectRoutes(oldS.Settings, newS.Settings) {
			allRoutes()
		}
	}

	if globalChanged {
		allRoutes()
	}

	// Routes affected through references (check both old and new bindings).
	for _, set := range [][]Route{oldS.Routes, newS.Routes} {
		for _, r := range set {
			if changedPolicies[r.IPPolicyID] || changedRelays[r.RelayID] {
				affected[r.ID] = true
			}
			for _, id := range r.RateLimitIDs {
				if changedRates[id] {
					affected[r.ID] = true
				}
			}
		}
	}

	for id := range affected {
		d.AffectedRoutes = append(d.AffectedRoutes, id)
	}
	sort.Strings(d.AffectedRoutes)
	if d.AffectedRoutes == nil {
		d.AffectedRoutes = []string{}
	}
	sortRefs(d.Added)
	sortRefs(d.Removed)
	sortRefs(d.Changed)
	return d
}

func sortRefs(r []Ref) {
	sort.Slice(r, func(i, j int) bool {
		if r[i].Kind != r[j].Kind {
			return r[i].Kind < r[j].Kind
		}
		return r[i].ID < r[j].ID
	})
}

// routeEqual ignores bookkeeping fields (timestamps, version) so that a
// re-save without changes is not reported as a change.
func routeEqual(a, b Route) bool {
	a.CreatedAt, a.UpdatedAt, a.Version = b.CreatedAt, b.UpdatedAt, b.Version
	return reflect.DeepEqual(a, b)
}

// settingsAffectRoutes reports whether a settings change requires routes to
// be re-applied (proxy ports, TLS, trusted proxies, GeoIP) as opposed to
// changes that only touch the daemon itself (log level, language, listen).
func settingsAffectRoutes(a, b Settings) bool {
	return !reflect.DeepEqual(a.LocalMode, b.LocalMode) ||
		!reflect.DeepEqual(a.TrustedProxies, b.TrustedProxies) ||
		a.GeoIP != b.GeoIP
}
