package api

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOpenAPIMatchesRoutes enforces that docs/openapi.yaml documents exactly the
// paths the server registers — no undocumented endpoints, no stale docs.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	documented := map[string]bool{}
	for p := range doc.Paths {
		documented[p] = true
	}
	registered := map[string]bool{}
	s := &Server{}
	for _, p := range s.Patterns() {
		registered[p] = true
	}

	for p := range registered {
		if !documented[p] {
			t.Errorf("route %s is registered but not in openapi.yaml", p)
		}
	}
	for p := range documented {
		if !registered[p] {
			t.Errorf("path %s is in openapi.yaml but not registered", p)
		}
	}
	if t.Failed() {
		var reg, doc2 []string
		for p := range registered {
			reg = append(reg, p)
		}
		for p := range documented {
			doc2 = append(doc2, p)
		}
		sort.Strings(reg)
		sort.Strings(doc2)
		t.Logf("registered: %v", reg)
		t.Logf("documented: %v", doc2)
	}
}
