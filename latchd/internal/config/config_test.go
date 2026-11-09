package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveCreatesRestrictedDirs(t *testing.T) {
	base := filepath.Join(t.TempDir(), "data")
	p, err := Resolve(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.DataDir, p.CaddyDir, p.LogDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("%s perm = %o, want 700", d, info.Mode().Perm())
		}
	}
	if p.DBPath != filepath.Join(base, "latchd.db") {
		t.Errorf("DBPath = %s", p.DBPath)
	}
}

func TestResolveTightensExistingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions not meaningful on windows")
	}
	base := filepath.Join(t.TempDir(), "loose")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(base); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(base)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("perm = %o, want 700", info.Mode().Perm())
	}
}

func TestDefaultDataDirEnvOverride(t *testing.T) {
	t.Setenv("LATCHD_DATA_DIR", "/tmp/x")
	d, err := DefaultDataDir()
	if err != nil || d != "/tmp/x" {
		t.Fatalf("got %q, %v", d, err)
	}
}
