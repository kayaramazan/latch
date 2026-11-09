// Package install builds the artifacts of `latch-relay install`: the config
// file, a systemd unit and a first agent connection string. The rendering is
// pure and unit-tested; the CLI adds interactive prompting and a DNS precheck.
package install

import (
	"fmt"
	"strings"

	"github.com/kayaramazan/latch/latch-relay/internal/config"
)

// Answers are the wizard inputs.
type Answers struct {
	PublicHost  string
	BaseDomain  string // defaults to PublicHost
	Email       string
	PublicIP    string
	DNSProvider string // "cloudflare" | "manual"
	Cloudflare  string // API token (cloudflare provider)
	Staging     bool

	DataDir   string
	HTTPSAddr string
	HTTPAddr  string
	AgentAddr string
	TCPPorts  string
}

// Config turns answers into a relay config.
func (a Answers) Config() config.Config {
	base := a.BaseDomain
	if base == "" {
		base = a.PublicHost
	}
	c := config.Config{
		PublicHost: a.PublicHost,
		BaseDomain: base,
		PublicIP:   a.PublicIP,
		DataDir:    a.DataDir,
		HTTPSAddr:  a.HTTPSAddr,
		HTTPAddr:   a.HTTPAddr,
		AgentAddr:  a.AgentAddr,
		TCPPorts:   a.TCPPorts,
		ACME: config.ACME{
			Enabled:         true,
			Email:           a.Email,
			Staging:         a.Staging,
			DNSProvider:     a.DNSProvider,
			CloudflareToken: a.Cloudflare,
		},
	}
	return c
}

// Validate checks the answers are sufficient to produce a working relay.
func (a Answers) Validate() error {
	if a.PublicHost == "" {
		return fmt.Errorf("public host is required")
	}
	if a.Email == "" {
		return fmt.Errorf("an email for Let's Encrypt is required")
	}
	switch a.DNSProvider {
	case "cloudflare":
		if a.Cloudflare == "" {
			return fmt.Errorf("a Cloudflare API token is required for the cloudflare provider")
		}
	case "manual", "":
	default:
		return fmt.Errorf("unknown DNS provider %q (want cloudflare or manual)", a.DNSProvider)
	}
	return nil
}

// SystemdOptions configure the unit file.
type SystemdOptions struct {
	BinPath    string // /usr/local/bin/latch-relay
	ConfigPath string // /etc/latch-relay/config.yaml
	User       string // service user (default latch-relay)
}

// RenderSystemdUnit renders a hardened systemd service. The service binds low
// ports via CAP_NET_BIND_SERVICE instead of running as root.
func RenderSystemdUnit(o SystemdOptions) string {
	if o.BinPath == "" {
		o.BinPath = "/usr/local/bin/latch-relay"
	}
	if o.ConfigPath == "" {
		o.ConfigPath = config.DefaultPath
	}
	if o.User == "" {
		o.User = "latch-relay"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\n")
	fmt.Fprintf(&b, "Description=Latch relay\n")
	fmt.Fprintf(&b, "After=network-online.target\n")
	fmt.Fprintf(&b, "Wants=network-online.target\n\n")
	fmt.Fprintf(&b, "[Service]\n")
	fmt.Fprintf(&b, "Type=simple\n")
	fmt.Fprintf(&b, "User=%s\n", o.User)
	fmt.Fprintf(&b, "ExecStart=%s run --config %s\n", o.BinPath, o.ConfigPath)
	fmt.Fprintf(&b, "Restart=on-failure\n")
	fmt.Fprintf(&b, "RestartSec=5\n")
	fmt.Fprintf(&b, "AmbientCapabilities=CAP_NET_BIND_SERVICE\n")
	fmt.Fprintf(&b, "CapabilityBoundingSet=CAP_NET_BIND_SERVICE\n")
	fmt.Fprintf(&b, "NoNewPrivileges=true\n")
	fmt.Fprintf(&b, "ProtectSystem=strict\n")
	fmt.Fprintf(&b, "ProtectHome=true\n")
	fmt.Fprintf(&b, "ReadWritePaths=/var/lib/latch-relay /etc/latch-relay\n")
	fmt.Fprintf(&b, "PrivateTmp=true\n\n")
	fmt.Fprintf(&b, "[Install]\n")
	fmt.Fprintf(&b, "WantedBy=multi-user.target\n")
	return b.String()
}

// WildcardProbe is the hostname the wizard resolves to confirm DNS points at
// this server before requesting a certificate.
func WildcardProbe(baseDomain string) string {
	return "acme-check." + baseDomain
}
