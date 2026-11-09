package tunnel

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ConnInfo is the parsed form of a latch:// connection string.
type ConnInfo struct {
	Host  string
	Port  int
	Token string
	CAPEM string // optional extra CA (dev)
	Name  string // optional display name
}

// DefaultAgentPort is the relay's agent listener port.
const DefaultAgentPort = 7443

// ParseConnString parses "latch://host:port/?token=...&ca=...&name=...".
func ParseConnString(s string) (*ConnInfo, error) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("tunnel: invalid connection string: %w", err)
	}
	if u.Scheme != "latch" {
		return nil, fmt.Errorf("tunnel: connection string must start with latch://")
	}
	host, portStr := u.Hostname(), u.Port()
	if host == "" {
		return nil, fmt.Errorf("tunnel: connection string has no host")
	}
	port := DefaultAgentPort
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("tunnel: invalid port %q", portStr)
		}
	}
	q := u.Query()
	info := &ConnInfo{Host: strings.ToLower(host), Port: port, Token: q.Get("token"), Name: q.Get("name")}
	if info.Token == "" {
		return nil, fmt.Errorf("tunnel: connection string has no token")
	}
	if ca := q.Get("ca"); ca != "" {
		pem, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(ca, "="))
		if err != nil {
			return nil, fmt.Errorf("tunnel: invalid ca parameter: %w", err)
		}
		if !strings.Contains(string(pem), "-----BEGIN CERTIFICATE-----") {
			return nil, fmt.Errorf("tunnel: ca parameter is not a PEM certificate")
		}
		info.CAPEM = string(pem)
	}
	return info, nil
}

// String formats the connection string.
func (c *ConnInfo) String() string {
	q := url.Values{}
	q.Set("token", c.Token)
	if c.CAPEM != "" {
		q.Set("ca", base64.RawURLEncoding.EncodeToString([]byte(c.CAPEM)))
	}
	if c.Name != "" {
		q.Set("name", c.Name)
	}
	port := c.Port
	if port == 0 {
		port = DefaultAgentPort
	}
	u := url.URL{Scheme: "latch", Host: net.JoinHostPort(c.Host, strconv.Itoa(port)), Path: "/", RawQuery: q.Encode()}
	return u.String()
}

// Addr returns host:port.
func (c *ConnInfo) Addr() string {
	port := c.Port
	if port == 0 {
		port = DefaultAgentPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}
