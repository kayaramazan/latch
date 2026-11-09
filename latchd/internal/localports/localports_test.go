package localports

import "testing"

func TestParseLsof(t *testing.T) {
	sample := "p1234\nnode\ncnode\nf10\nn*:3000\np1234\nf11\nn127.0.0.1:5432\np5678\ncpostgres\nf3\nn[::1]:5433\n"
	// Rebuild properly: p starts a process block, c is command, n is a name.
	sample = "p1234\ncnode\nf10\nn*:3000\nf11\nn127.0.0.1:5432\np5678\ncpostgres\nf3\nn[::1]:5433\n"
	ports := dedupe(parseLsof(sample))
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d: %+v", len(ports), ports)
	}
	byPort := map[int]Port{}
	for _, p := range ports {
		byPort[p.Port] = p
	}
	if byPort[3000].Address != "127.0.0.1:3000" || byPort[3000].Process != "node" {
		t.Errorf("3000 = %+v", byPort[3000])
	}
	if byPort[5432].Address != "127.0.0.1:5432" {
		t.Errorf("5432 = %+v", byPort[5432])
	}
	if byPort[5433].Address != "127.0.0.1:5433" || byPort[5433].Process != "postgres" {
		t.Errorf("5433 (ipv6 loopback → 127.0.0.1) = %+v", byPort[5433])
	}
}

func TestPortFromName(t *testing.T) {
	cases := map[string]int{"*:3000": 3000, "127.0.0.1:8080": 8080, "[::1]:5433": 5433, "localhost:9000": 9000}
	for name, want := range cases {
		if p, ok := portFromName(name); !ok || p != want {
			t.Errorf("portFromName(%q) = %d,%v want %d", name, p, ok, want)
		}
	}
	if _, ok := portFromName("noport"); ok {
		t.Error("noport should fail")
	}
}

func TestDialTargetWildcard(t *testing.T) {
	if dialTarget("*:3000", 3000) != "127.0.0.1:3000" {
		t.Error("wildcard should map to loopback")
	}
	if dialTarget("192.168.1.5:80", 80) != "192.168.1.5:80" {
		t.Error("specific host preserved")
	}
}
