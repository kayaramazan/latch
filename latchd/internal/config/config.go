// Package config resolves where latchd keeps its data and enforces file
// permissions. Settings that users edit live in the store, not here.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the on-disk locations latchd uses.
type Paths struct {
	DataDir  string // 0700
	DBPath   string // DataDir/latchd.db, 0600
	CaddyDir string // DataDir/caddy (certificates, ACME state)
	LogDir   string // DataDir/logs
}

// DefaultDataDir returns the platform default data directory, honouring
// LATCHD_DATA_DIR when set.
func DefaultDataDir() (string, error) {
	if v := os.Getenv("LATCHD_DATA_DIR"); v != "" {
		return v, nil
	}
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "latchd"), nil
	case "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "latchd"), nil
	default:
		if os.Geteuid() == 0 {
			return "/var/lib/latchd", nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "latchd"), nil
	}
}

// Resolve builds Paths for dataDir (empty = platform default) and creates the
// directories with restrictive permissions. Existing directories with looser
// permissions are tightened.
func Resolve(dataDir string) (Paths, error) {
	var err error
	if dataDir == "" {
		dataDir, err = DefaultDataDir()
		if err != nil {
			return Paths{}, fmt.Errorf("config: resolve data dir: %w", err)
		}
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, err
	}
	p := Paths{
		DataDir:  dataDir,
		DBPath:   filepath.Join(dataDir, "latchd.db"),
		CaddyDir: filepath.Join(dataDir, "caddy"),
		LogDir:   filepath.Join(dataDir, "logs"),
	}
	for _, d := range []string{p.DataDir, p.CaddyDir, p.LogDir} {
		if err := ensureDir(d); err != nil {
			return Paths{}, err
		}
	}
	return p, nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("config: chmod %s: %w", dir, err)
		}
	}
	return nil
}
