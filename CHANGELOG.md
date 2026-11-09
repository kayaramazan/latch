# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Marketing landing page** for signed-out visitors (`panel-ui`): hero, how-it-works,
  feature and pricing sections, with calls to action to sign in, create an account,
  or start an anonymous trial. The dashboard is shown once authenticated.
- **Anonymous free trial** ("try without signing up"): `POST /api/trial` creates a
  short-lived guest account (Free plan, 1-hour expiry) and session so the dashboard
  works immediately; `POST /api/claim` converts a guest into a full account by
  setting an email + password (keeping its tunnels). A background reaper suspends
  expired trials, which revokes their tunnels on the fleet. Guardrails: per-IP rate
  limit on trial creation, Free-plan hard caps, short expiry. The dashboard shows a
  trial banner with the remaining time and an inline "save my account" form. New
  store methods `SetAccountTrial` and `SetUserCredentials` (the latter also fixes
  `UpdateUser`, which never persisted email/password changes).
- **SaaS customer dashboard** (`latch-control/panel-ui`): a React + TypeScript +
  Tailwind + TanStack Query single-page app, built into
  `latch-control/internal/panel/dist` and served by the control plane as the
  customer-facing control panel (replacing the single minimal `index.html`).
  Screens: email/password + TOTP auth; Overview; Tunnels (create a tunnel, reveal
  the one-time connection string with ready-to-paste agent commands, revoke);
  custom Domains (add/verify/delete, plan-gated); Usage (bandwidth/tunnels/domains
  against plan limits); Billing (plan catalogue + Merchant-of-Record checkout);
  Download (agent install + connect instructions); Account (2FA setup/enable, API
  keys, data export, account deletion, sign-out). CSRF + same-origin enforced as
  before. New read-only `GET /api/plans` exposes the plan catalogue to the UI.
  `make panel-install` / `panel-build` / `test-panel`.

### Fixed

- `latch-relay install` now creates the dedicated `latch-relay` system user and
  gives it the data directory. The generated systemd unit runs as that user, but
  the wizard previously never created it, so `systemctl start latch-relay` failed
  on a fresh host with `status=217/USER` ("Unknown user"). The step is
  best-effort (Linux + root) and prints the manual `useradd`/`chown` commands if
  it cannot run. `DefaultServiceUser` is now the single source for the account
  name shared by the unit template and the installer.

## [1.0.0] - 2026-11-09

First stable release. The self-hosted product (reverse proxy + self-hosted
tunnel, managed from a web UI) is complete and hardened, and the managed-relay
SaaS control plane (S0–S3) is functional on top of it. Security checklist closed
(docs/SECURITY_CHECKLIST.md), the three SPEC acceptance scenarios pass
(docs/ACCEPTANCE.md), the protocol codec and connection-string parser are fuzzed
with no crashes, and a load test is recorded (deploy/loadtest/RESULTS.md).


- Phase 9: web UI (latch-ui). A Vite + React + TypeScript + Tailwind + TanStack
  Query + Recharts single-page app, served by latchd from an embedded build
  (go:embed) at the same origin as the API, with an SPA fallback for client-side
  routes. Screens: login, dashboard (req/s, p50/p95/p99, status mix, top routes,
  top blocked), routes list with health colours, the two-question add-route
  wizard (target → tunnel/relay or local), route detail with policy binding, IP
  policies with a "does this IP pass?" tester, rate limits, relays (connect by
  pasting a latch:// string, "set up your relay" wizard, live sync state), a live
  event stream (SSE), config history with rollback and backup export/import,
  settings, and explain. Full English + Turkish i18n with a CI check that fails
  on any missing/empty key. Auth-aware client (login → session cookie + CSRF).
  Tests: Vitest for the add-route wizard, Playwright for the tunnel-add flow
  (CI). `make ui-build` embeds the bundle; the UI, SPA fallback and same-origin
  API were verified with the real binary. Decisions: plain Tailwind (shadcn-like,
  no component-lib dependency); dark theme follows the OS. See docs/DEV.md.
- Phase 8: management API (REST + WebSocket) and authentication. The agent API is
  now complete and documented: config get/versions/diff/rollback and a dry-run
  `POST /api/config/validate` (field errors + affected routes, nothing saved);
  backup export/import (replace|merge); settings; system; relay setup helpers
  (`setup-script`, `dns-check`); and a WebSocket `/api/ws` streaming events,
  status, health and config-version changes. Authentication: an argon2id
  management password (`latchd auth set-password`) → session cookie (HttpOnly,
  SameSite=Strict) + CSRF token; mutations require the CSRF token when auth is
  on, cross-origin mutations are always refused, logins are rate-limited, and the
  API refuses to bind a non-loopback address without a password. `docs/openapi.yaml`
  (OpenAPI 3) is kept in lockstep with the code by a test that fails if the
  registered routes and documented paths diverge (the CI check). Verified with the
  real binary: unauthenticated → 401, login → 200, mutation without CSRF → 403,
  with CSRF → 201, `/api/system` always open. See docs/DEV.md.
- Phase 7: observability. `latchd/internal/events` is a unified event hub — every
  AccessEvent (local or relay) flows through one sink into an in-memory ring (for
  live `--follow`) and a SQLite store (for query and dashboards), with async
  batched writes and age+size retention that drops rather than crashes on a full
  queue or disk trouble. Query by time range, route, IP, status, decision, reason
  or text. Server-side dashboard aggregation (`/api/dashboard`): req/s, p50/p95/p99
  latency, status classes, top routes, currently-blocked, top blocked IPs last
  hour. `policy.Explain` traces a sample request through the rules (order, where,
  result); `latchd explain` / `POST /api/explain`. latchd Prometheus `/metrics`
  on the management API (requests, denials, per-route traffic, latency, tunnel and
  health gauges). Structured JSON logs to a rotated file (`--log-file`). CLI:
  `latchd events --follow --route`, `latchd explain`. `deploy/grafana/` ships a
  scrape config and a sample dashboard. Verified with the real binaries: tunnel
  and local requests share one source-tagged stream, `/api/dashboard` aggregates
  them, and `/metrics` is scrapeable. See docs/DEV.md.
- Phase 6: local mode — embedded Caddy + the same policies on the agent. The
  one `latchd` binary can now be a TLS reverse proxy on a public-IP server
  without writing Caddy config. `latchd/internal/local`: a model→Caddy-JSON
  generator (reverse_proxy, path routing, auto TLS via ACME or Caddy's internal
  CA, custom cert files, health checks, HTTP/3) with structure/golden tests;
  Caddy v2 embedded as a library (standard bundle + filestorage), applied with
  `caddy.Load` (zero-downtime; the previous config survives an error and the
  daemon never crashes); a custom `latch_policy` Caddy handler that runs the
  shared /policy engine and emits local AccessEvents; an upstream health checker
  for both modes (status in /api/status); and listen-port conflict detection.
  `latchd route add --local --hostname … --target …`. Verified end to end: an
  HTTPS local site served via Caddy's internal CA (certificate verified, not
  skipped), response header rules applied, and the same IP policy that the relay
  enforces denying a request (403) with a local AccessEvent — "one policy, same
  result in both modes". See docs/DEV.md.
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
