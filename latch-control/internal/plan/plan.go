// Package plan is latch-control's plan catalog and quota limits. Plans are code
// (not rows): a small fixed catalog keyed by id, attached to an account by
// Account.PlanID. See docs/SAAS_PRICING.md.
package plan

// Plan is a subscription tier and its quota limits.
type Plan struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	PriceCents       int    `json:"priceCents"`
	Currency         string `json:"currency"`
	MaxTokens        int    `json:"maxTokens"`
	MaxDomains       int    `json:"maxDomains"`
	MaxConcurrent    int    `json:"maxConcurrent"`
	BandwidthGBMonth int    `json:"bandwidthGbMonth"`
	RequestsPerMin   int    `json:"requestsPerMin"`
	// HardCapBandwidth cuts traffic at 100% of the bandwidth allowance (free);
	// paid plans meter overage instead.
	HardCapBandwidth  bool `json:"hardCapBandwidth"`
	OverageCentsPerGB int  `json:"overageCentsPerGb"`
}

// BandwidthBytes returns the monthly bandwidth allowance in bytes.
func (p Plan) BandwidthBytes() int64 { return int64(p.BandwidthGBMonth) * 1024 * 1024 * 1024 }

// Catalog is the fixed set of plans.
var catalog = map[string]Plan{
	"free": {ID: "free", Name: "Free", PriceCents: 0, Currency: "USD",
		MaxTokens: 1, MaxDomains: 0, MaxConcurrent: 1, BandwidthGBMonth: 5, RequestsPerMin: 60, HardCapBandwidth: true},
	"pro": {ID: "pro", Name: "Pro", PriceCents: 900, Currency: "USD",
		MaxTokens: 10, MaxDomains: 3, MaxConcurrent: 10, BandwidthGBMonth: 100, RequestsPerMin: 600, OverageCentsPerGB: 10},
	"team": {ID: "team", Name: "Team", PriceCents: 2900, Currency: "USD",
		MaxTokens: 50, MaxDomains: 25, MaxConcurrent: 50, BandwidthGBMonth: 1024, RequestsPerMin: 1200, OverageCentsPerGB: 8},
}

// Get returns a plan by id, falling back to free for unknown ids.
func Get(id string) Plan {
	if p, ok := catalog[id]; ok {
		return p
	}
	return catalog["free"]
}

// All returns the catalog, sorted free→pro→team.
func All() []Plan { return []Plan{catalog["free"], catalog["pro"], catalog["team"]} }

// SoftThreshold is the fraction of an allowance at which to warn.
const SoftThreshold = 0.8
