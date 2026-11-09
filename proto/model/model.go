// Package model holds the data types shared by the agent (latchd) and the
// relay (latch-relay). Field names, defaults and rules are specified in
// docs/DATA_MODEL.md; this file is the executable form of that document.
package model

import (
	"strings"
	"time"
)

// Exposure says where a route is published from.
type Exposure string

const (
	ExposureLocal  Exposure = "local"
	ExposureTunnel Exposure = "tunnel"
)

// RouteKind is the layer a route operates on.
type RouteKind string

const (
	KindHTTP RouteKind = "http"
	KindTCP  RouteKind = "tcp"
)

// TLSMode controls certificate handling for local routes.
type TLSMode string

const (
	TLSAuto   TLSMode = "auto"
	TLSCustom TLSMode = "custom"
	TLSOff    TLSMode = "off"
)

// Route maps one or more hostnames (or a TCP port) to a local target.
type Route struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Enabled       bool         `json:"enabled"`
	Target        string       `json:"target"`
	Exposure      Exposure     `json:"exposure"`
	Hostnames     []string     `json:"hostnames"`
	RelayID       string       `json:"relayId,omitempty"`
	Kind          RouteKind    `json:"kind"`
	TCPPort       int          `json:"tcpPort,omitempty"`
	PathPrefix    string       `json:"pathPrefix"`
	TLS           TLSMode      `json:"tls"`
	TLSCertFile   string       `json:"tlsCertFile,omitempty"`
	TLSKeyFile    string       `json:"tlsKeyFile,omitempty"`
	HTTPSRedirect bool         `json:"httpsRedirect"`
	Headers       []HeaderRule `json:"headers"`
	HealthCheck   HealthCheck  `json:"healthCheck"`
	IPPolicyID    string       `json:"ipPolicyId,omitempty"`
	RateLimitIDs  []string     `json:"rateLimitIds"`
	ProxyProtocol bool         `json:"proxyProtocol,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	Version       int          `json:"version"`
}

// NewRoute returns a route with an ID and every default applied.
func NewRoute(name, target string, exposure Exposure) Route {
	now := time.Now().UTC()
	r := Route{
		ID:            NewID(PrefixRoute),
		Name:          name,
		Enabled:       true,
		Target:        target,
		Exposure:      exposure,
		HTTPSRedirect: true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	r.Normalize()
	return r
}

// Normalize fills zero-valued fields with their defaults and canonicalizes
// user input (lowercase hostnames, trimmed strings). It never fails; Validate
// reports what is still wrong afterwards.
func (r *Route) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.Target = strings.TrimSpace(r.Target)
	if r.Kind == "" {
		r.Kind = KindHTTP
	}
	if r.PathPrefix == "" {
		r.PathPrefix = "/"
	}
	if r.TLS == "" {
		r.TLS = TLSAuto
	}
	for i, h := range r.Hostnames {
		r.Hostnames[i] = strings.ToLower(strings.TrimSpace(h))
	}
	if r.Hostnames == nil {
		r.Hostnames = []string{}
	}
	if r.Headers == nil {
		r.Headers = []HeaderRule{}
	}
	if r.RateLimitIDs == nil {
		r.RateLimitIDs = []string{}
	}
	r.HealthCheck.normalize(r.Kind)
}

// HeaderPhase selects request or response headers.
type HeaderPhase string

const (
	HeaderRequest  HeaderPhase = "request"
	HeaderResponse HeaderPhase = "response"
)

// HeaderOp is what a HeaderRule does.
type HeaderOp string

const (
	HeaderSet    HeaderOp = "set"
	HeaderAdd    HeaderOp = "add"
	HeaderDelete HeaderOp = "delete"
)

// HeaderRule adds, sets or deletes one header.
type HeaderRule struct {
	Phase HeaderPhase `json:"phase"`
	Op    HeaderOp    `json:"op"`
	Name  string      `json:"name"`
	Value string      `json:"value,omitempty"`
}

// HealthCheck configures upstream probing.
type HealthCheck struct {
	Enabled        bool   `json:"enabled"`
	Path           string `json:"path,omitempty"`
	IntervalSec    int    `json:"intervalSec"`
	TimeoutSec     int    `json:"timeoutSec"`
	ExpectStatus   []int  `json:"expectStatus,omitempty"`
	UnhealthyAfter int    `json:"unhealthyAfter"`
	HealthyAfter   int    `json:"healthyAfter"`
}

func (h *HealthCheck) normalize(kind RouteKind) {
	if h.IntervalSec == 0 {
		h.IntervalSec = 10
	}
	if h.TimeoutSec == 0 {
		h.TimeoutSec = 3
	}
	if h.UnhealthyAfter == 0 {
		h.UnhealthyAfter = 3
	}
	if h.HealthyAfter == 0 {
		h.HealthyAfter = 1
	}
	if kind == KindHTTP {
		if h.Path == "" {
			h.Path = "/"
		}
		if len(h.ExpectStatus) == 0 {
			h.ExpectStatus = []int{200, 399}
		}
	} else {
		h.Path = ""
		h.ExpectStatus = nil
	}
}

// Action is an allow/deny verdict.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

// DenyMode selects how a denied request is answered.
type DenyMode string

const (
	DenyStatus DenyMode = "status"
	DenyDrop   DenyMode = "drop"
)

// DenyBehavior is what the enforcement point does on deny.
type DenyBehavior struct {
	Mode   DenyMode `json:"mode"`
	Status int      `json:"status,omitempty"`
}

// IPRule matches either a CIDR or a country; exactly one must be set.
type IPRule struct {
	Action  Action `json:"action"`
	CIDR    string `json:"cidr,omitempty"`
	Country string `json:"country,omitempty"`
}

// IPPolicy is an ordered allow/deny list with a default action.
type IPPolicy struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	DefaultAction Action       `json:"defaultAction"`
	Rules         []IPRule     `json:"rules"`
	OnDeny        DenyBehavior `json:"onDeny"`
	Global        bool         `json:"global"`
}

// NewIPPolicy returns a policy with an ID and defaults (allow by default, 403 on deny).
func NewIPPolicy(name string) IPPolicy {
	p := IPPolicy{ID: NewID(PrefixIPPolicy), Name: name}
	p.Normalize()
	return p
}

// Normalize applies defaults and canonicalizes rule input.
func (p *IPPolicy) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	if p.DefaultAction == "" {
		p.DefaultAction = ActionAllow
	}
	if p.OnDeny.Mode == "" {
		p.OnDeny = DenyBehavior{Mode: DenyStatus, Status: 403}
	}
	if p.OnDeny.Mode == DenyStatus && p.OnDeny.Status == 0 {
		p.OnDeny.Status = 403
	}
	if p.Rules == nil {
		p.Rules = []IPRule{}
	}
	for i := range p.Rules {
		p.Rules[i].CIDR = strings.TrimSpace(p.Rules[i].CIDR)
		p.Rules[i].Country = strings.ToUpper(strings.TrimSpace(p.Rules[i].Country))
		if ip := p.Rules[i].CIDR; ip != "" && !strings.Contains(ip, "/") {
			if strings.Contains(ip, ":") {
				p.Rules[i].CIDR = ip + "/128"
			} else {
				p.Rules[i].CIDR = ip + "/32"
			}
		}
	}
}

// ExceedMode selects what happens when a rate limit is exceeded.
type ExceedMode string

const (
	ExceedReject ExceedMode = "reject"
	ExceedDelay  ExceedMode = "delay"
)

// ExceedBehavior configures the response to a limited request.
type ExceedBehavior struct {
	Mode       ExceedMode `json:"mode"`
	MaxDelayMs int        `json:"maxDelayMs,omitempty"`
}

// RateLimitRule is a sliding-window limit keyed by request attributes.
type RateLimitRule struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Keys      []string       `json:"keys"`
	Limit     int            `json:"limit"`
	WindowSec int            `json:"windowSec"`
	OnExceed  ExceedBehavior `json:"onExceed"`
	Global    bool           `json:"global"`
}

// NewRateLimitRule returns a rule with an ID and defaults (per-IP, reject).
func NewRateLimitRule(name string, limit, windowSec int) RateLimitRule {
	r := RateLimitRule{ID: NewID(PrefixRateLimit), Name: name, Limit: limit, WindowSec: windowSec}
	r.Normalize()
	return r
}

// Normalize applies defaults.
func (r *RateLimitRule) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	if len(r.Keys) == 0 {
		r.Keys = []string{"ip"}
	}
	if r.OnExceed.Mode == "" {
		r.OnExceed.Mode = ExceedReject
	}
	if r.OnExceed.Mode == ExceedDelay && r.OnExceed.MaxDelayMs == 0 {
		r.OnExceed.MaxDelayMs = 2000
	}
}

// Relay is a relay server the agent connects to.
type Relay struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Token      string `json:"token"`
	CAPEM      string `json:"caPem,omitempty"`
	BaseDomain string `json:"baseDomain,omitempty"`
}

// DefaultRelayPort is the relay's agent listener port.
const DefaultRelayPort = 7443

// NewRelay returns a relay with an ID and defaults.
func NewRelay(name, host, token string) Relay {
	r := Relay{ID: NewID(PrefixRelay), Name: name, Host: host, Token: token}
	r.Normalize()
	return r
}

// Normalize applies defaults.
func (r *Relay) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.Host = strings.ToLower(strings.TrimSpace(r.Host))
	r.BaseDomain = strings.ToLower(strings.TrimSpace(r.BaseDomain))
	if r.Port == 0 {
		r.Port = DefaultRelayPort
	}
}

// AuthMode is how the management API authenticates.
type AuthMode string

const (
	AuthNone     AuthMode = "none"
	AuthPassword AuthMode = "password"
)

// Settings is the single global settings object.
type Settings struct {
	Listen         string            `json:"listen"`
	Auth           AuthSettings      `json:"auth"`
	TrustedProxies []string          `json:"trustedProxies"`
	LocalMode      LocalModeSettings `json:"localMode"`
	GeoIP          GeoIPSettings     `json:"geoip"`
	Events         EventSettings     `json:"events"`
	Log            LogSettings       `json:"log"`
	Language       string            `json:"language"`
}

// AuthSettings configures management API authentication.
type AuthSettings struct {
	Mode         AuthMode `json:"mode"`
	PasswordHash string   `json:"passwordHash,omitempty"`
}

// LocalModeSettings configures the embedded proxy.
type LocalModeSettings struct {
	HTTPPort  int    `json:"httpPort"`
	HTTPSPort int    `json:"httpsPort"`
	HTTP3     bool   `json:"http3"`
	ACMEEmail string `json:"acmeEmail"`
	ACMECA    string `json:"acmeCa"`
}

// GeoIPSettings points to the MaxMind-format database.
type GeoIPSettings struct {
	DBPath string `json:"dbPath"`
}

// EventSettings controls event retention.
type EventSettings struct {
	RetentionDays int `json:"retentionDays"`
	MaxSizeMB     int `json:"maxSizeMb"`
}

// LogSettings controls the daemon log.
type LogSettings struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// Known ACME CA names for LocalModeSettings.ACMECA.
const (
	ACMELetsEncrypt        = "letsencrypt"
	ACMELetsEncryptStaging = "letsencrypt-staging"
	ACMEZeroSSL            = "zerossl"
)

// DefaultSettings returns the settings a fresh install starts with.
func DefaultSettings() Settings {
	return Settings{
		Listen:         "127.0.0.1:4040",
		Auth:           AuthSettings{Mode: AuthNone},
		TrustedProxies: []string{},
		LocalMode:      LocalModeSettings{HTTPPort: 80, HTTPSPort: 443, HTTP3: true, ACMECA: ACMELetsEncrypt},
		Events:         EventSettings{RetentionDays: 7, MaxSizeMB: 500},
		Log:            LogSettings{Level: "info", Format: "json"},
		Language:       "en",
	}
}

// Normalize applies defaults to unset fields. Boolean fields (HTTP3) cannot be
// defaulted this way; new settings objects must start from DefaultSettings().
func (s *Settings) Normalize() {
	d := DefaultSettings()
	if s.Listen == "" {
		s.Listen = d.Listen
	}
	if s.Auth.Mode == "" {
		s.Auth.Mode = AuthNone
	}
	if s.TrustedProxies == nil {
		s.TrustedProxies = []string{}
	}
	if s.LocalMode.HTTPPort == 0 {
		s.LocalMode.HTTPPort = d.LocalMode.HTTPPort
	}
	if s.LocalMode.HTTPSPort == 0 {
		s.LocalMode.HTTPSPort = d.LocalMode.HTTPSPort
	}
	if s.LocalMode.ACMECA == "" {
		s.LocalMode.ACMECA = d.LocalMode.ACMECA
	}
	if s.Events.RetentionDays == 0 {
		s.Events.RetentionDays = d.Events.RetentionDays
	}
	if s.Events.MaxSizeMB == 0 {
		s.Events.MaxSizeMB = d.Events.MaxSizeMB
	}
	if s.Log.Level == "" {
		s.Log.Level = d.Log.Level
	}
	if s.Log.Format == "" {
		s.Log.Format = d.Log.Format
	}
	if s.Language == "" {
		s.Language = d.Language
	}
}

// Snapshot is the entire configuration at one version.
type Snapshot struct {
	Routes     []Route         `json:"routes"`
	IPPolicies []IPPolicy      `json:"ipPolicies"`
	RateLimits []RateLimitRule `json:"rateLimits"`
	Relays     []Relay         `json:"relays"`
	Settings   Settings        `json:"settings"`
}

// NewSnapshot returns an empty configuration with default settings.
func NewSnapshot() *Snapshot {
	return &Snapshot{
		Routes:     []Route{},
		IPPolicies: []IPPolicy{},
		RateLimits: []RateLimitRule{},
		Relays:     []Relay{},
		Settings:   DefaultSettings(),
	}
}

// Normalize applies defaults to every object in the snapshot.
func (s *Snapshot) Normalize() {
	if s.Routes == nil {
		s.Routes = []Route{}
	}
	if s.IPPolicies == nil {
		s.IPPolicies = []IPPolicy{}
	}
	if s.RateLimits == nil {
		s.RateLimits = []RateLimitRule{}
	}
	if s.Relays == nil {
		s.Relays = []Relay{}
	}
	for i := range s.Routes {
		s.Routes[i].Normalize()
	}
	for i := range s.IPPolicies {
		s.IPPolicies[i].Normalize()
	}
	for i := range s.RateLimits {
		s.RateLimits[i].Normalize()
	}
	for i := range s.Relays {
		s.Relays[i].Normalize()
	}
	s.Settings.Normalize()
}

// Lookup helpers. They return nil when the ID is unknown.

// Route returns the route with the given ID.
func (s *Snapshot) Route(id string) *Route {
	for i := range s.Routes {
		if s.Routes[i].ID == id {
			return &s.Routes[i]
		}
	}
	return nil
}

// IPPolicy returns the policy with the given ID.
func (s *Snapshot) IPPolicy(id string) *IPPolicy {
	for i := range s.IPPolicies {
		if s.IPPolicies[i].ID == id {
			return &s.IPPolicies[i]
		}
	}
	return nil
}

// RateLimit returns the rate limit rule with the given ID.
func (s *Snapshot) RateLimit(id string) *RateLimitRule {
	for i := range s.RateLimits {
		if s.RateLimits[i].ID == id {
			return &s.RateLimits[i]
		}
	}
	return nil
}

// Relay returns the relay with the given ID.
func (s *Snapshot) Relay(id string) *Relay {
	for i := range s.Relays {
		if s.Relays[i].ID == id {
			return &s.Relays[i]
		}
	}
	return nil
}
