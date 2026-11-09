// Package service installs latchd as a system service (systemd on Linux,
// launchd on macOS). It renders the unit/plist (pure, testable) and performs
// the install/uninstall steps.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Options configure the service.
type Options struct {
	BinPath string // latchd binary path (default: the running executable)
	DataDir string // --data-dir for the service
	Listen  string // --listen for the service
	User    string // service user (Linux); empty = root
	LogFile string // optional --log-file
}

const (
	unitPath  = "/etc/systemd/system/latchd.service"
	plistPath = "/Library/LaunchDaemons/com.latch.latchd.plist"
	plistName = "com.latch.latchd"
)

func (o *Options) defaults() {
	if o.BinPath == "" {
		if p, err := os.Executable(); err == nil {
			o.BinPath = p
		} else {
			o.BinPath = "/usr/local/bin/latchd"
		}
	}
}

// RenderSystemd renders the systemd unit.
func RenderSystemd(o Options) string {
	o.defaults()
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Latch agent\nAfter=network-online.target\nWants=network-online.target\n\n")
	fmt.Fprintf(&b, "[Service]\nType=simple\n")
	if o.User != "" {
		fmt.Fprintf(&b, "User=%s\n", o.User)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", execStart(o))
	fmt.Fprintf(&b, "Restart=on-failure\nRestartSec=5\nNoNewPrivileges=true\nProtectSystem=strict\n")
	dataDir := o.DataDir
	if dataDir == "" {
		dataDir = "/var/lib/latchd"
	}
	fmt.Fprintf(&b, "ReadWritePaths=%s\nStateDirectory=latchd\nPrivateTmp=true\n\n", dataDir)
	fmt.Fprintf(&b, "[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// RenderLaunchd renders the launchd plist.
func RenderLaunchd(o Options) string {
	o.defaults()
	args := []string{o.BinPath, "run"}
	if o.DataDir != "" {
		args = append(args, "--data-dir", o.DataDir)
	}
	if o.Listen != "" {
		args = append(args, "--listen", o.Listen)
	}
	if o.LogFile != "" {
		args = append(args, "--log-file", o.LogFile)
	}
	var a strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&a, "    <string>%s</string>\n", arg)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>
%s  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
`, plistName, a.String())
}

func execStart(o Options) string {
	parts := []string{o.BinPath, "run"}
	if o.DataDir != "" {
		parts = append(parts, "--data-dir", o.DataDir)
	}
	if o.Listen != "" {
		parts = append(parts, "--listen", o.Listen)
	}
	if o.LogFile != "" {
		parts = append(parts, "--log-file", o.LogFile)
	}
	return strings.Join(parts, " ")
}

// Install writes the service file and enables it.
func Install(o Options) error {
	o.defaults()
	switch runtime.GOOS {
	case "linux":
		if err := writeFile(unitPath, RenderSystemd(o), 0o644); err != nil {
			return err
		}
		run("systemctl", "daemon-reload")
		run("systemctl", "enable", "--now", "latchd")
		fmt.Printf("Installed %s and started the latchd service.\n", unitPath)
		fmt.Println("Status: systemctl status latchd   Logs: journalctl -u latchd -f")
	case "darwin":
		if err := writeFile(plistPath, RenderLaunchd(o), 0o644); err != nil {
			return err
		}
		run("launchctl", "load", "-w", plistPath)
		fmt.Printf("Installed %s and loaded the latchd service.\n", plistPath)
	default:
		return fmt.Errorf("service install is not supported on %s yet; run `latchd run` under your init system", runtime.GOOS)
	}
	return nil
}

// Uninstall stops and removes the service.
func Uninstall() error {
	switch runtime.GOOS {
	case "linux":
		run("systemctl", "disable", "--now", "latchd")
		_ = os.Remove(unitPath)
		run("systemctl", "daemon-reload")
		fmt.Println("Removed the latchd service.")
	case "darwin":
		run("launchctl", "unload", "-w", plistPath)
		_ = os.Remove(plistPath)
		fmt.Println("Removed the latchd service.")
	default:
		return fmt.Errorf("service uninstall is not supported on %s", runtime.GOOS)
	}
	return nil
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

// run executes a command, ignoring the error (best-effort service ops) but
// surfacing it to stderr.
func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s %s: %v\n", name, strings.Join(args, " "), err)
	}
}
