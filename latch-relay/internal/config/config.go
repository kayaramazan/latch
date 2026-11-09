// Package config is the relay's on-disk configuration file (YAML), written by
// `latch-relay install` and read by `latch-relay run`. Command-line flags
// override file values.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the install wizard writes the config on Linux.
const DefaultPath = "/etc/latch-relay/config.yaml"

// Config is the relay configuration file.
type Config struct {
	PublicHost string `yaml:"public_host"`
	BaseDomain string `yaml:"base_domain"`
	PublicIP   string `yaml:"public_ip,omitempty"`
	DataDir    string `yaml:"data_dir,omitempty"`

	HTTPSAddr string `yaml:"https_addr,omitempty"`
	HTTPAddr  string `yaml:"http_addr,omitempty"`
	AgentAddr string `yaml:"agent_addr,omitempty"`
	TCPPorts  string `yaml:"tcp_ports,omitempty"`
	TCPBind   string `yaml:"tcp_bind,omitempty"`

	ACME ACME `yaml:"acme"`

	LogLevel  string `yaml:"log_level,omitempty"`
	LogFormat string `yaml:"log_format,omitempty"`
}

// ACME holds certificate-automation settings.
type ACME struct {
	Enabled         bool   `yaml:"enabled"`
	Email           string `yaml:"email,omitempty"`
	Staging         bool   `yaml:"staging,omitempty"`
	DNSProvider     string `yaml:"dns_provider,omitempty"`     // cloudflare | manual
	CloudflareToken string `yaml:"cloudflare_token,omitempty"` // may also come from env
	CADirURL        string `yaml:"ca_dir_url,omitempty"`       // override (Pebble/testing)
}

// CloudflareTokenEnv is consulted when the token is not in the file.
const CloudflareTokenEnv = "CLOUDFLARE_API_TOKEN"

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	c.applyDefaults()
	if c.ACME.CloudflareToken == "" {
		c.ACME.CloudflareToken = os.Getenv(CloudflareTokenEnv)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.BaseDomain == "" {
		c.BaseDomain = c.PublicHost
	}
	if c.HTTPSAddr == "" {
		c.HTTPSAddr = ":443"
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":80"
	}
	if c.AgentAddr == "" {
		c.AgentAddr = ":7443"
	}
	if c.TCPPorts == "" {
		c.TCPPorts = "20000-20999"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogFormat == "" {
		c.LogFormat = "json"
	}
}

// Validate checks the config is internally consistent.
func (c *Config) Validate() error {
	if c.PublicHost == "" {
		return fmt.Errorf("config: public_host is required")
	}
	if c.ACME.Enabled {
		if c.ACME.Email == "" {
			return fmt.Errorf("config: acme.email is required when acme.enabled")
		}
		switch c.ACME.DNSProvider {
		case "cloudflare":
			if c.ACME.CloudflareToken == "" {
				return fmt.Errorf("config: acme.cloudflare_token (or $%s) is required for the cloudflare provider", CloudflareTokenEnv)
			}
		case "manual", "":
		default:
			return fmt.Errorf("config: unknown acme.dns_provider %q (want cloudflare or manual)", c.ACME.DNSProvider)
		}
	}
	return nil
}

// Marshal renders the config as YAML with a header comment.
func (c *Config) Marshal() ([]byte, error) {
	b, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	header := "# latch-relay configuration — see `latch-relay install`\n# Secrets here are sensitive; keep this file 0600.\n"
	return append([]byte(header), b...), nil
}

// Write saves the config to path with 0600 permissions.
func (c *Config) Write(path string) error {
	b, err := c.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
