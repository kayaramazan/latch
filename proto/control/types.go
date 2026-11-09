// Package control is the shared contract for the relay control API: the
// private, mTLS, flag-gated interface the SaaS control plane uses to drive a
// relay (create/revoke tokens and custom domains, read sessions, health and
// usage). Self-hosted relays never enable it. See docs/SAAS_ARCHITECTURE.md.
package control

import "time"

// APIVersion is the control API path prefix.
const APIVersion = "/control/v1"

// CreateTokenRequest asks the relay to mint an agent token.
type CreateTokenRequest struct {
	Name      string   `json:"name"`
	Hostnames []string `json:"hostnames,omitempty"`
	TCP       bool     `json:"tcp,omitempty"`
	ExpiresIn string   `json:"expiresIn,omitempty"` // Go duration, "" = never
}

// TokenInfo describes a token.
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hostnames  []string   `json:"hostnames,omitempty"`
	TCP        bool       `json:"tcp,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// CreateTokenResponse carries the once-shown connection string.
type CreateTokenResponse struct {
	Token            TokenInfo `json:"token"`
	ConnectionString string    `json:"connectionString"`
}

// CreateDomainRequest registers a custom domain.
type CreateDomainRequest struct {
	Domain string `json:"domain"`
	Note   string `json:"note,omitempty"`
}

// DomainInfo describes a custom domain.
type DomainInfo struct {
	Domain     string     `json:"domain"`
	Target     string     `json:"target"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// VerifyDomainResponse is the DNS verification outcome.
type VerifyDomainResponse struct {
	Domain   string `json:"domain"`
	Verified bool   `json:"verified"`
	Detail   string `json:"detail,omitempty"`
}

// SessionInfo mirrors one live agent session.
type SessionInfo struct {
	ID          string    `json:"id"`
	TokenID     string    `json:"tokenId"`
	TokenName   string    `json:"tokenName"`
	AgentID     string    `json:"agentId"`
	RemoteAddr  string    `json:"remoteAddr"`
	ConnectedAt time.Time `json:"connectedAt"`
	RTTMs       float64   `json:"rttMs"`
}

// Usage is the relay's cumulative usage counters (monotonic since start).
type Usage struct {
	BytesIn     int64 `json:"bytesIn"`
	BytesOut    int64 `json:"bytesOut"`
	Requests    int64 `json:"requests"`
	Denials     int64 `json:"denials"`
	Connections int64 `json:"connections"` // total accepted streams
}

// Health is the relay's self-report (also the heartbeat payload body).
type Health struct {
	RelayID    string           `json:"relayId"`
	Region     string           `json:"region"`
	Version    string           `json:"version"`
	PublicHost string           `json:"publicHost"`
	BaseDomain string           `json:"baseDomain"`
	Agents     int              `json:"agents"`
	Routes     int              `json:"routes"`
	Tokens     int              `json:"tokens"`
	Usage      Usage            `json:"usage"`
	TokenUsage map[string]Usage `json:"tokenUsage,omitempty"`
	ReportedAt time.Time        `json:"reportedAt"`
}

// Heartbeat is what a relay pushes to the control plane (register + liveness +
// usage in one message).
type Heartbeat struct {
	RelayID    string `json:"relayId"`
	Region     string `json:"region"`
	Version    string `json:"version"`
	ControlURL string `json:"controlUrl"` // where the control plane reaches the relay's control API
	Capacity   int    `json:"capacity"`
	Health     Health `json:"health"`
}
