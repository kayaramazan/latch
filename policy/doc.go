// Package policy is the shared policy engine (client IP resolution, IP/CIDR
// and GeoIP allow/deny, sliding-window rate limiting, header rules, explain).
// It runs inside the relay (tunnel mode) and inside latchd as a Caddy handler
// (local mode). Implemented in phase 5.
package policy
