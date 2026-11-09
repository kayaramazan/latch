package model

import "time"

// EventSource says which enforcement point produced an access event.
type EventSource string

const (
	SourceLocal EventSource = "local"
	SourceRelay EventSource = "relay"
)

// Decision is the outcome of policy evaluation for one request.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// Deny reasons used in AccessEvent.DenyReason.
const (
	DenyIPPolicy     = "ip_policy"
	DenyGeo          = "geo"
	DenyRateLimit    = "rate_limit"
	DenyAgentOffline = "agent_offline"
	DenyUnknownHost  = "unknown_host"
	DenyUpstream     = "upstream_error"
)

// AccessEvent is one proxied (or refused) request, emitted by both the relay
// and the local proxy. See docs/DATA_MODEL.md.
type AccessEvent struct {
	TS         time.Time   `json:"ts"`
	Source     EventSource `json:"source"`
	RelayID    string      `json:"relayId,omitempty"`
	RouteID    string      `json:"routeId,omitempty"`
	Hostname   string      `json:"hostname"`
	ClientIP   string      `json:"clientIp"`
	Country    string      `json:"country,omitempty"`
	Method     string      `json:"method,omitempty"`
	Path       string      `json:"path,omitempty"`
	Status     int         `json:"status"`
	DurationMs int64       `json:"durationMs"`
	BytesIn    int64       `json:"bytesIn"`
	BytesOut   int64       `json:"bytesOut"`
	Target     string      `json:"target,omitempty"`
	Decision   Decision    `json:"decision"`
	DenyReason string      `json:"denyReason,omitempty"`
	RuleID     string      `json:"ruleId,omitempty"`
	Where      string      `json:"where"` // "relay" or "agent"
}
