# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

- Phase 5: relay policies (IP, GeoIP, rate limit, headers) + PolicySync +
  AccessEvent. Shared `/policy` package: trusted-proxy client-IP resolution,
  IP/CIDR and GeoIP (MaxMind mmdb) allow/deny with a documented global/route
  precedence, an LRU-bounded sliding-window rate limiter, header rules, and an
  Evaluate/Decision API (used by the relay now, Caddy in phase 6). Relay: a
  policies layer that persists each token's PolicySync in SQLite and enforces it
  at ingress before a request reaches the agent (and after restart / while the
  agent is offline); `/metrics` (Prometheus) on `--metrics-addr`. Protocol:
  PolicySync is acknowledged with its version; AccessEvents stream relay→agent
  as batched frames. Agent (`latchd`): sends a full PolicySync per relay on
  connect and on config change, tracks the ack as an in_sync/out_of_sync state,
  buffers incoming AccessEvents in a ring, and exposes `latchd events [--follow]`
  (plus `/api/events` and an SSE stream). Minimal policy CRUD on the agent API
  (`/api/ip-policies`, `/api/rate-limits`, route binding fields). Verified end to
  end with the real binaries (IP deny → 403 at the relay, visible in
  `latchd events` and `/metrics`). See docs/DEV.md.
- Phase 4: relay TLS/ACME, setup wizard, custom domains. certmagic integration
  (`latch-relay/internal/acme`): a wildcard certificate for the base domain via
  ACME DNS-01 (Cloudflare or a manual-TXT mode) and on-demand certificates for
  registered custom domains via TLS-ALPN-01. Abuse-guarding permission function —
  a custom domain gets a certificate only when the operator registered it and its
  DNS points at the relay (CNAME to the public host or A record to the public IP);
  unit-tested for registered/unregistered/wrong-DNS cases. New relay-store
  `custom_domains` table and `latch-relay domain add/list/rm/verify`. Relay YAML
  config (`/etc/latch-relay/config.yaml`, read by `run`, flags override).
  `latch-relay install` wizard (config + hardened systemd unit + first token +
  connection string, with a DNS precheck), `latch-relay status`, and
  `latch-relay doctor` (DNS, ports, clock). `deploy/install.sh` one-line installer.
  `make test-acme` runs a Pebble integration test behind the `acme_pebble` build
  tag (CI-only). Agent-side :7443 verification already uses system roots (dev CA
  only when explicitly provided). See docs/DEV.md.
- Phase 3: agent tunnel client — first end-to-end. `latchd/internal/tunnel`:
  a client per configured relay that dials over verifying TLS (system roots plus
  the relay's optional dev CA; verification is never skipped), performs the
  Hello/HelloAck handshake with version negotiation, announces the store's
  tunnel routes (full set), serves relay-opened data streams by dialing the
  local target and copying bytes both ways, answers ping/pong, and reconnects
  with exponential backoff + jitter (1s→60s). A manager keeps one client per
  relay in sync with the config store, re-announcing on route changes and
  restarting a client when a relay's connection settings change. Management REST
  API (`latchd/internal/api`, loopback-only, no auth yet): routes/relays CRUD and
  `/api/status`. `latchd` CLI (`latchd/internal/cli`): `run`, `relay add/list/rm`,
  `route add/list/rm`, `status` — the subcommands are thin clients of the local
  API. Persistent agent id. End-to-end tests (relay + latchd + local server in
  one process): HTTP with forwarded client IP, WebSocket passthrough, local
  target down → 502, relay outage → automatic reconnect < 10s, version-mismatch
  reporting. Verified against the real binaries with the dev CA (see docs/DEV.md).
- Phase 2: tunnel protocol and relay core. `proto/tunnel` (control-message
  envelope + length-prefixed JSON codec, data-stream header/status, connection
  string, version negotiation, yamux config; fuzzed). latch-relay: SQLite store
  (tokens, routes, settings), argon2id token auth with scopes, hostname/TCP-port
  registry with full-set announce and offline persistence, agent session
  (handshake, ping/pong, stream opening, supersede/limits), ingress (reverse
  proxy over tunnel streams, TCP tunnels, 404/502/503 pages, connection limits),
  dev CA, and the `latch-relay` CLI (run, token create/list/revoke, dev-ca).
  Integration tests cover HTTP/WebSocket/TCP end to end, scope/conflict
  rejection, supersede/revoke and restart persistence. See docs/DEV.md.
- Phase 1: shared data model (`proto/model`: types, defaults, whole-snapshot
  validation with field-level errors, diff with affected-route computation) and
  latchd config store (`latchd/internal/store`: SQLite via modernc, migrations,
  immutable config versions, rollback, diff, export/import replace|merge,
  change subscriptions, pruning) plus data-dir resolution (`latchd/internal/config`).
- Project renamed from Gate to Latch.
- Phase 0: design documents (docs/ARCHITECTURE.md, docs/DATA_MODEL.md,
  PROTOCOL.md draft, docs/API.md, docs/SCREENS.md, docs/SECURITY.md), repository
  skeleton (go.mod, placeholder binaries, Makefile, CI, lint config, license),
  README. No feature code yet.
- Project planning: SPEC.md, ROADMAP.md, CLAUDE.md, NOTES.md.
