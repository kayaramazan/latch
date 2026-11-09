package local

import (
	"github.com/kayaramazan/latch/policy"
	"github.com/kayaramazan/latch/proto/model"
)

// localPolicyInputs collects the IP policies, rate limits and per-route
// bindings that apply to the snapshot's local routes (plus all globals).
func localPolicyInputs(snap *model.Snapshot) ([]model.IPPolicy, []model.RateLimitRule, map[string]policy.Binding) {
	neededIP := map[string]bool{}
	neededRL := map[string]bool{}
	bindings := map[string]policy.Binding{}
	for _, rt := range snap.Routes {
		if rt.Exposure != model.ExposureLocal || !rt.Enabled {
			continue
		}
		bindings[rt.ID] = policy.Binding{IPPolicyID: rt.IPPolicyID, RateLimitIDs: rt.RateLimitIDs, Headers: rt.Headers}
		if rt.IPPolicyID != "" {
			neededIP[rt.IPPolicyID] = true
		}
		for _, id := range rt.RateLimitIDs {
			neededRL[id] = true
		}
	}
	var ips []model.IPPolicy
	for _, p := range snap.IPPolicies {
		if p.Global || neededIP[p.ID] {
			ips = append(ips, p)
		}
	}
	var rls []model.RateLimitRule
	for _, r := range snap.RateLimits {
		if r.Global || neededRL[r.ID] {
			rls = append(rls, r)
		}
	}
	return ips, rls, bindings
}
