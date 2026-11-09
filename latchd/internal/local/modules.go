package local

// Blank imports register the Caddy modules the agent's local mode loads at
// runtime: the standard module bundle (http, reverse_proxy, tls with the
// internal issuer, headers, …) plus the file-system storage module, which the
// standard bundle does not include. The custom latch_policy handler registers
// itself in policyhandler.go.
import (
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)
