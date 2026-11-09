package policy

import (
	"net/http"

	"github.com/kayaramazan/latch/proto/model"
)

// ApplyHeaderRules applies the header rules for the given phase to h.
func ApplyHeaderRules(h http.Header, rules []model.HeaderRule, phase model.HeaderPhase) {
	for _, r := range rules {
		if r.Phase != phase || r.Name == "" {
			continue
		}
		switch r.Op {
		case model.HeaderSet:
			h.Set(r.Name, r.Value)
		case model.HeaderAdd:
			h.Add(r.Name, r.Value)
		case model.HeaderDelete:
			h.Del(r.Name)
		}
	}
}
