package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func clone(s *Snapshot) *Snapshot {
	b, _ := json.Marshal(s)
	var c Snapshot
	_ = json.Unmarshal(b, &c)
	return &c
}

func TestDiffNoChanges(t *testing.T) {
	a := validSnapshot()
	b := clone(a)
	d := ComputeDiff(a, b)
	if !d.Empty() || len(d.AffectedRoutes) != 0 || d.Summary() != "no changes" {
		t.Errorf("expected empty diff, got %+v", d)
	}
	b.Routes[0].Version = 99
	b.Routes[0].UpdatedAt = b.Routes[0].UpdatedAt.AddDate(0, 0, 1)
	if d := ComputeDiff(a, b); !d.Empty() {
		t.Errorf("bookkeeping fields should be ignored: %+v", d)
	}
}

func TestDiffAffectedRoutes(t *testing.T) {
	base := validSnapshot()
	site, app, ssh := base.Routes[0].ID, base.Routes[1].ID, base.Routes[2].ID

	cases := []struct {
		name   string
		mutate func(s *Snapshot)
		want   []string
		kinds  []Ref
	}{
		{"route changed", func(s *Snapshot) { s.Routes[1].Target = "localhost:3001" }, []string{app}, []Ref{{KindRoute, app, "app"}}},
		{"route added", func(s *Snapshot) {
			r := NewRoute("new", "127.0.0.1:1", ExposureLocal)
			r.ID = "rt_aaaaaaaaaaaaaaaaaaaaaaaaaa"
			r.Hostnames = []string{"new.example.com"}
			s.Routes = append(s.Routes, r)
		}, []string{site, app, ssh, "rt_aaaaaaaaaaaaaaaaaaaaaaaaaa"}[3:], nil},
		{"route removed", func(s *Snapshot) { s.Routes = s.Routes[1:] }, []string{site}, nil},
		{"policy changed affects bound route", func(s *Snapshot) { s.IPPolicies[0].Rules = nil }, []string{site}, nil},
		{"policy made global affects all", func(s *Snapshot) { s.IPPolicies[0].Global = true }, []string{site, app, ssh}, nil},
		{"rate limit changed", func(s *Snapshot) { s.RateLimits[0].Limit = 5 }, []string{site}, nil},
		{"relay changed affects tunnel routes", func(s *Snapshot) { s.Relays[0].Port = 8443 }, []string{app, ssh}, nil},
		{"rebind policy affects route", func(s *Snapshot) { s.Routes[0].IPPolicyID = "" }, []string{site}, nil},
		{"settings log level affects nothing", func(s *Snapshot) { s.Settings.Log.Level = "debug" }, []string{}, []Ref{{Kind: KindSettings}}},
		{"settings local mode affects all", func(s *Snapshot) { s.Settings.LocalMode.HTTP3 = false }, []string{site, app, ssh}, nil},
		{"settings trusted proxies affects all", func(s *Snapshot) { s.Settings.TrustedProxies = []string{"10.0.0.1"} }, []string{site, app, ssh}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := clone(base)
			tc.mutate(b)
			d := ComputeDiff(base, b)
			want := append([]string{}, tc.want...)
			sortStrings(want)
			if !reflect.DeepEqual(d.AffectedRoutes, want) {
				t.Errorf("affected = %v, want %v (diff %+v)", d.AffectedRoutes, want, d)
			}
			if tc.kinds != nil && !reflect.DeepEqual(d.Changed, tc.kinds) && !reflect.DeepEqual(d.Added, tc.kinds) {
				t.Errorf("refs = changed %v added %v, want %v", d.Changed, d.Added, tc.kinds)
			}
		})
	}
}

func TestDiffRemovedPolicyAffectsOldBinding(t *testing.T) {
	a := validSnapshot()
	b := clone(a)
	b.IPPolicies = nil
	b.Routes[0].IPPolicyID = ""
	d := ComputeDiff(a, b)
	if len(d.Removed) != 1 || d.Removed[0].Kind != KindIPPolicy {
		t.Errorf("removed = %v", d.Removed)
	}
	if !reflect.DeepEqual(d.AffectedRoutes, []string{a.Routes[0].ID}) {
		t.Errorf("affected = %v", d.AffectedRoutes)
	}
	if d.Summary() != "1 changed, 1 removed" {
		t.Errorf("summary = %q", d.Summary())
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
