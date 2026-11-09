package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kayaramazan/latch/proto/model"
)

func TestRecordAndExpose(t *testing.T) {
	m := New()
	m.Record(model.AccessEvent{Decision: model.DecisionAllow, Status: 200, BytesOut: 100})
	m.Record(model.AccessEvent{Decision: model.DecisionDeny, Status: 403, DenyReason: model.DenyIPPolicy})
	m.SetAgents(2)

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	out := string(body)
	for _, want := range []string{
		"latch_relay_requests_total",
		`latch_relay_denied_total{reason="ip_policy"} 1`,
		"latch_relay_agents 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
