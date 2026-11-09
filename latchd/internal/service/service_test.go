package service

import (
	"strings"
	"testing"
)

func TestRenderSystemd(t *testing.T) {
	u := RenderSystemd(Options{BinPath: "/usr/local/bin/latchd", DataDir: "/var/lib/latchd", User: "latchd"})
	for _, want := range []string{
		"ExecStart=/usr/local/bin/latchd run --data-dir /var/lib/latchd",
		"User=latchd", "NoNewPrivileges=true", "WantedBy=multi-user.target",
		"ReadWritePaths=/var/lib/latchd",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("systemd unit missing %q\n%s", want, u)
		}
	}
}

func TestRenderLaunchd(t *testing.T) {
	p := RenderLaunchd(Options{BinPath: "/usr/local/bin/latchd", Listen: "127.0.0.1:4040"})
	for _, want := range []string{
		"com.latch.latchd", "<string>/usr/local/bin/latchd</string>", "<string>run</string>",
		"<string>--listen</string>", "<string>127.0.0.1:4040</string>", "<key>RunAtLoad</key><true/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("launchd plist missing %q\n%s", want, p)
		}
	}
}
