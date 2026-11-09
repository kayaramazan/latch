package cli

import "github.com/kayaramazan/latch/proto/model"

// routeInput mirrors the management API's route body (pointer fields so unset
// fields are omitted / left unchanged). JSON tags must match api.routeInput.
type routeInput struct {
	Name       *string          `json:"name,omitempty"`
	Target     *string          `json:"target,omitempty"`
	Exposure   *model.Exposure  `json:"exposure,omitempty"`
	Kind       *model.RouteKind `json:"kind,omitempty"`
	RelayID    *string          `json:"relayId,omitempty"`
	Hostnames  *[]string        `json:"hostnames,omitempty"`
	PathPrefix *string          `json:"pathPrefix,omitempty"`
	TCPPort    *int             `json:"tcpPort,omitempty"`
	Enabled    *bool            `json:"enabled,omitempty"`
}

// relayInput mirrors the management API's relay body. JSON tags must match
// api.relayInput.
type relayInput struct {
	Name       *string `json:"name,omitempty"`
	Host       *string `json:"host,omitempty"`
	Port       *int    `json:"port,omitempty"`
	Token      *string `json:"token,omitempty"`
	CAPEM      *string `json:"caPem,omitempty"`
	BaseDomain *string `json:"baseDomain,omitempty"`
}
