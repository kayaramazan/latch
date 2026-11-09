package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/kayaramazan/latch/latchd/internal/service"
)

// Install implements `latchd install`.
func Install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory for the service")
	listen := fs.String("listen", "", "management API listen address")
	user := fs.String("user", "", "service user (Linux; default root)")
	logFile := fs.String("log-file", "", "write structured logs to this file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return service.Install(service.Options{DataDir: *dataDir, Listen: *listen, User: *user, LogFile: *logFile})
}

// Uninstall implements `latchd uninstall`.
func Uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return service.Uninstall()
}

// Upgrade implements `latchd upgrade`: download a release binary, replace this
// one, and restart the service.
func Upgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	version := fs.String("version", "latest", "release version (e.g. v1.2.3)")
	baseURL := fs.String("base-url", "https://github.com/kayaramazan/latch/releases", "release base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	asset := fmt.Sprintf("latchd_%s_%s", runtime.GOOS, arch())
	url := *baseURL + "/latest/download/" + asset
	if *version != "latest" {
		url = *baseURL + "/download/" + *version + "/" + asset
	}
	fmt.Printf("Downloading %s…\n", url)
	tmp, err := download(url, filepath.Dir(self))
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, self); err != nil {
		return fmt.Errorf("replace %s (try with sudo): %w", self, err)
	}
	fmt.Printf("Upgraded %s.\n", self)
	restartService()
	return nil
}

func arch() string {
	if runtime.GOARCH == "amd64" {
		return "amd64"
	}
	return "arm64"
}

func download(url, dir string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("download failed: " + resp.Status)
	}
	f, err := os.CreateTemp(dir, "latchd-upgrade-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, resp.Body)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func restartService() {
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("systemctl", "restart", "latchd").Run()
	case "darwin":
		_ = exec.Command("launchctl", "kickstart", "-k", "system/com.latch.latchd").Run()
	}
}
