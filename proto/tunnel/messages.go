// Package tunnel defines the Latch tunnel protocol: control-stream messages,
// framing, data-stream headers and the connection string. PROTOCOL.md is the
// specification; this package is its executable form and is shared by latchd
// and latch-relay.
package tunnel

import (
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

// Protocol versions supported by this build.
const (
	ProtocolVersion    = 1
	MinProtocolVersion = 1
)

// ALPN is the TLS application protocol name for agent connections.
const ALPN = "latch/1"

// Type is a control message type.
type Type string

// Control message types.
const (
	TypeHello         Type = "Hello"
	TypeHelloAck      Type = "HelloAck"
	TypeRouteAnnounce Type = "RouteAnnounce"
	TypeRouteAck      Type = "RouteAck"
	TypeRouteWithdraw Type = "RouteWithdraw"
	TypePolicySync    Type = "PolicySync"
	TypePolicyAck     Type = "PolicyAck"
	TypePing          Type = "Ping"
	TypePong          Type = "Pong"
	TypeAccessEvent   Type = "AccessEvent"
	TypeError         Type = "Error"
	TypeGoodbye       Type = "Goodbye"
)

// Capabilities advertised in Hello/HelloAck.
const (
	CapTCP    = "tcp"
	CapEvents = "events"
	CapGeoIP  = "geoip"
)

// Hello is the first message, agent → relay.
type Hello struct {
	ProtoMin     int      `json:"protoMin"`
	ProtoMax     int      `json:"protoMax"`
	AgentVersion string   `json:"agentVersion"`
	AgentID      string   `json:"agentId"`
	Token        string   `json:"token"`
	Capabilities []string `json:"capabilities"`
}

// Scope is what a token may publish.
type Scope struct {
	Hostnames    []string `json:"hostnames"`
	TCP          bool     `json:"tcp"`
	TCPPortRange []int    `json:"tcpPortRange,omitempty"`
}

// HelloAck is the relay's answer to Hello.
type HelloAck struct {
	Proto         int       `json:"proto"`
	RelayVersion  string    `json:"relayVersion"`
	ServerTime    time.Time `json:"serverTime"`
	Scope         Scope     `json:"scope"`
	BaseDomain    string    `json:"baseDomain,omitempty"`
	PolicyVersion int       `json:"policyVersion"`
	Capabilities  []string  `json:"capabilities"`
}

// RouteSpec is one route in a RouteAnnounce.
type RouteSpec struct {
	RouteID    string          `json:"routeId"`
	Kind       model.RouteKind `json:"kind"`
	Hostnames  []string        `json:"hostnames,omitempty"`
	PathPrefix string          `json:"pathPrefix,omitempty"`
	TCPPort    int             `json:"tcpPort,omitempty"`
}

// RouteAnnounce is the agent's full set of tunnel routes. Routes not listed
// are withdrawn.
type RouteAnnounce struct {
	Routes []RouteSpec `json:"routes"`
}

// RouteAccepted reports a route the relay now serves.
type RouteAccepted struct {
	RouteID    string   `json:"routeId"`
	Hostnames  []string `json:"hostnames,omitempty"`
	TCPPort    int      `json:"tcpPort,omitempty"`
	PublicURLs []string `json:"publicUrls"`
}

// RouteRejected reports a route the relay refused.
type RouteRejected struct {
	RouteID string `json:"routeId"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RouteAck answers a RouteAnnounce.
type RouteAck struct {
	Accepted []RouteAccepted `json:"accepted"`
	Rejected []RouteRejected `json:"rejected"`
}

// RouteWithdraw removes routes without re-announcing the full set.
type RouteWithdraw struct {
	RouteIDs []string `json:"routeIds"`
}

// PolicyBinding attaches policies and header rules to one route.
type PolicyBinding struct {
	RouteID      string             `json:"routeId"`
	IPPolicyID   string             `json:"ipPolicyId,omitempty"`
	RateLimitIDs []string           `json:"rateLimitIds,omitempty"`
	Headers      []model.HeaderRule `json:"headers,omitempty"`
}

// PolicySync is the agent's full policy snapshot for this token.
type PolicySync struct {
	Version    int                   `json:"version"`
	IPPolicies []model.IPPolicy      `json:"ipPolicies"`
	RateLimits []model.RateLimitRule `json:"rateLimits"`
	Bindings   []PolicyBinding       `json:"bindings"`
}

// PolicyAck confirms the version the relay now enforces.
type PolicyAck struct {
	Version int `json:"version"`
}

// Ping and Pong carry the sender's timestamp for RTT measurement.
type Ping struct {
	TS time.Time `json:"ts"`
}

// Pong answers a Ping with the Ping's timestamp.
type Pong struct {
	TS time.Time `json:"ts"`
}

// AccessEventBatch streams relay-side events to the agent.
type AccessEventBatch struct {
	Events  []model.AccessEvent `json:"events"`
	Dropped int                 `json:"dropped,omitempty"`
}

// Error reports a protocol or application error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
	// RelayMin/RelayMax are set with ErrVersion so the agent can explain
	// which side to update.
	RelayMin int `json:"relayMin,omitempty"`
	RelayMax int `json:"relayMax,omitempty"`
}

func (e *Error) Error() string {
	if e.Fatal {
		return e.Code + " (fatal): " + e.Message
	}
	return e.Code + ": " + e.Message
}

// Goodbye announces a graceful close.
type Goodbye struct {
	Reason string `json:"reason"`
}

// Goodbye reasons.
const (
	ByeShutdown     = "shutdown"
	ByeSuperseded   = "superseded"
	ByeTokenRevoked = "token_revoked"
	ByeReload       = "reload"
)

// Error codes. See PROTOCOL.md for fatality.
const (
	ErrAuth               = "ERR_AUTH"
	ErrVersion            = "ERR_VERSION"
	ErrCodeFrameTooLarge  = "ERR_FRAME_TOO_LARGE"
	ErrMalformed          = "ERR_MALFORMED"
	ErrTimeout            = "ERR_TIMEOUT"
	ErrUnknownType        = "ERR_UNKNOWN_TYPE"
	ErrInvalid            = "ERR_INVALID"
	ErrScope              = "ERR_SCOPE"
	ErrHostnameTaken      = "ERR_HOSTNAME_TAKEN"
	ErrTCPPortUnavailable = "ERR_TCP_PORT_UNAVAILABLE"
	ErrTCPNotAllowed      = "ERR_TCP_NOT_ALLOWED"
	ErrRateLimited        = "ERR_RATE_LIMITED"
	ErrSuperseded         = "ERR_SUPERSEDED"
	ErrTooManySessions    = "ERR_TOO_MANY_SESSIONS"
	ErrInternal           = "ERR_INTERNAL"
)

// StreamHeader is the first thing the relay writes on a data stream.
type StreamHeader struct {
	RouteID    string          `json:"routeId"`
	Kind       model.RouteKind `json:"kind"`
	Hostname   string          `json:"hostname,omitempty"`
	ClientIP   string          `json:"clientIp"`
	ClientPort int             `json:"clientPort"`
	TLS        bool            `json:"tls"`
	SNI        string          `json:"sni,omitempty"`
	StreamID   string          `json:"streamId"`
}

// Status is the one byte the agent writes after reading a StreamHeader.
type Status byte

// Stream status values.
const (
	StatusOK         Status = 0x00
	StatusDialFailed Status = 0x01
	StatusNoRoute    Status = 0x02
	StatusBusy       Status = 0x03
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusDialFailed:
		return "dial_failed"
	case StatusNoRoute:
		return "no_route"
	case StatusBusy:
		return "busy"
	default:
		return "unknown"
	}
}

// Timing constants from PROTOCOL.md.
const (
	HelloTimeout      = 10 * time.Second
	PingInterval      = 15 * time.Second
	PingDeadAfter     = 45 * time.Second
	StreamOpenTimeout = 10 * time.Second
)

// Negotiate picks the highest protocol version both sides support. ok is
// false when there is no overlap.
func Negotiate(peerMin, peerMax int) (version int, ok bool) {
	hi := min(peerMax, ProtocolVersion)
	lo := max(peerMin, MinProtocolVersion)
	if lo > hi {
		return 0, false
	}
	return hi, true
}
