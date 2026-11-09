# NOTES

Decisions, assumptions and backlog. Newest first within each section.

## Decisions

- 2026-11-09 — Go module path `github.com/kayaramazan/latch`, single `go.mod` at the
  repo root (shared `proto/` and `policy/` packages import naturally; no workspace).
  License Apache-2.0. User decision.
- 2026-11-09 — Tunnel control messages are length-prefixed JSON (u32 BE + JSON,
  1 MiB cap); data streams are raw bytes after a small JSON header. Why: simplest
  to debug and document; the envelope is versioned so protobuf can replace it
  later without changing the transport. Alternative: protobuf (needs buf/protoc
  toolchain). User decision.
- 2026-11-09 — GeoIP in tunnel mode: the relay reads a local `.mmdb` file
  configured in `/etc/latch-relay/config.yaml` (optionally downloaded by
  `latch-relay install`). `HelloAck.capabilities` tells the agent whether `geoip`
  is available; the UI warns when country rules are attached but the relay lacks
  the DB. Alternative (shipping the DB through the tunnel) deferred to v1+. User decision.
- 2026-11-09 — UI package manager pnpm 9 (installed locally, lockfile committed).
  Node 22 LTS. Smallest safe default; no user impact.
- 2026-11-09 — `go.mod` says `go 1.24` although the dev machine has 1.26: 1.24 is
  the floor Caddy v2.10 needs; SPEC's "1.22+" is satisfied as a minimum we do
  not go below. CI uses `go-version-file: go.mod`.
- 2026-11-09 — Relay has no network admin API at all (CLI only); everything from
  the agent goes through the authenticated tunnel. Tightest reading of SPEC
  "Relay yönetim API'si varsayılan kapalı".
- 2026-11-09 — HTTP/2 terminates at the relay; the tunnel carries HTTP/1.1 to the
  agent, and the agent speaks HTTP/1.1 to the local target (v1). WebSocket
  passes through. gRPC over tunnel is therefore v1+ (needs h2c end-to-end).
- 2026-11-09 — Secrets on the agent (relay tokens, DNS API keys) are stored in
  `latchd.db` with `0600` permissions; OS keychain integration is v1+.
- 2026-11-09 — latchd management listener default `127.0.0.1:4040`; relay metrics
  `127.0.0.1:7070`; relay TCP tunnel pool `20000-20999`. Arbitrary but documented;
  all configurable.

- 2026-11-09 — Project renamed from "Gate" to "Latch". Binaries: `latchd` (agent),
  `latch-relay` (relay), `latch-ui` (web UI); connection string scheme `latch://`.
  Why: "gate" is too generic (name collisions on GitHub/npm/Go modules) and
  `gated` reads as an English adjective. Alternatives: keep Gate.
  Open: verify GitHub org/repo and domain availability before first publish.

## Backlog

- gRPC / h2c end-to-end through the tunnel (needs HTTP/2 to the agent). v1+.
- OS keychain for agent-side secrets. v1+.
- GeoIP DB upload from the UI to the relay over the tunnel. v1+.
- UI mobile layout. v1+.
- Multiple sessions of one token load-balancing (currently newest wins). v1+.
