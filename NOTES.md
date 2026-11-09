# NOTES

Decisions, assumptions and backlog. Newest first within each section.

## Decisions

- 2026-11-09 — Phase 2 — HTTP forwarding uses `httputil.ReverseProxy` over a
  custom `DialContext` that opens a yamux stream per request, not a raw byte
  hijack. Why: the public side gets HTTP/2 and WebSocket handling from stdlib,
  and the policy engine (phase 5) plugs in as ordinary middleware. The relay
  therefore terminates HTTP and speaks HTTP/1.1 (`Connection: close`, one stream
  per request) to the agent. PROTOCOL.md updated to match. Alternative (raw
  hijack) deferred; revisit only if per-request stream overhead matters.
- 2026-11-09 — Agent tokens are `lt_<id>_<secret>` where both parts use a
  base32 lowercase alphabet (no `_`/`-`/padding). A base64url encoding (default
  choice) silently broke `split()` because `_` is in its alphabet and also the
  field delimiter — a ~1-in-5 flaky auth failure. Regression test:
  `TestTokenEncodingHasNoDelimiter`.
- 2026-11-09 — yamux `StreamOpenTimeout` is disabled (0): it closes the whole
  session when any stream stays pending. Latch bounds each `OpenStream` with its
  own deadline and uses control-stream Ping/Pong (15s/45s) for liveness.
- 2026-11-09 — Relay has no network admin API; TLS in phase 2 is a static cert
  (`--tls-cert/--tls-key`) or a dev CA (`--dev`). Automatic ACME is phase 4.
- 2026-11-09 — "Same token, same agentId" supersedes the older session
  (`Goodbye{superseded}`); up to 4 sessions per token otherwise. A revoked or
  expired token is noticed by a 30s store poll (`watchRevocations`) as well as
  immediately on `RevokeToken`, because the CLI revokes in a separate process.

- 2026-11-09 — Store keeps the **full snapshot JSON per version** (not deltas) and
  prunes to 200 versions by default (`store.Options.MaxVersions`). Config is small;
  simplicity and trivial rollback win.
- 2026-11-09 — Store opens SQLite with `SetMaxOpenConns(1)` + WAL + busy_timeout.
  Single-process, write-serialized by a mutex; no need for a connection pool and it
  rules out SQLITE_BUSY under `-race` tests. Revisit if the event store (phase 7)
  needs concurrent readers — it can use its own pool.
- 2026-11-09 — `Normalize()` cannot default boolean fields (`Route.Enabled`,
  `Settings.LocalMode.HTTP3`, `HTTPSRedirect`): a zero bool is indistinguishable
  from "false set". Rule: new objects are created through `NewRoute/NewRelay/
  DefaultSettings`, and the API layer (phase 8) must merge PATCH bodies onto the
  existing object rather than onto zero values.
- 2026-11-09 — Route bookkeeping (`UpdatedAt`, `Version`) is stamped by the store
  only when the route's content changed, so re-saving the snapshot does not churn
  versions; `ComputeDiff` ignores those fields.
- 2026-11-09 — Hostname rule: lowercase FQDN with ≥2 labels, no wildcards, no IP
  literals. Wildcards belong to relay token scopes, not routes.

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

- 2026-11-09 — (Faz 3) Reconnect backoff: exponential 1s→60s, factor 2, full
  jitter ±20% (random 0.8–1.2×). Backoff resets to 1s on every successful
  handshake, so a flapping relay recovers fast while a dead one is polled at
  most once a minute. Arbitrary but matches common agent defaults; configurable
  later if needed.
- 2026-11-09 — (Faz 3) The agent is a byte pipe on data streams: it reads the
  StreamHeader, dials the local target, writes a 1-byte status, then copies raw
  bytes both ways. It does not parse HTTP, so HTTP/1.1, WebSocket and raw TCP
  all work through the same path. HTTP semantics live on the relay.
- 2026-11-09 — (Faz 3) RouteAnnounce is always the full set for a relay. A route
  add/remove/enable in the store pushes the new full set to the matching relay
  client, which re-announces; the relay's full-set semantics withdraw whatever
  is gone. No separate RouteWithdraw diffing on the agent in v1.
- 2026-11-09 — (Faz 3) One hostname is bound to exactly one relay because it
  lives on exactly one route (route.relayId), and model validation forbids
  duplicate HTTP hostnames across routes. So "announce the same hostname to two
  relays" cannot arise from a valid config; no extra agent-side guard needed.
  Decision point from ROADMAP resolved: rely on model validation, not a new rule.
- 2026-11-09 — (Faz 3) Management API binds loopback only and has NO auth in this
  phase (127.0.0.1:4040). ListenAndServe refuses any non-loopback bind until
  phase 8 adds authentication. Agent id is persisted in `<data-dir>/agent_id`
  (0600) so reconnects supersede the previous session cleanly.
- 2026-11-09 — (Faz 3) latchd CLI subcommands (relay/route/status) are thin HTTP
  clients of the local API (default http://127.0.0.1:4040, $LATCHD_API or --api
  to override); only `run` touches the store directly. Keeps one source of truth
  (the daemon) and makes the CLI usable while the UI does not exist yet.

- 2026-11-09 — (Faz 4) certmagic is the certificate provider. Two ACME issuers
  share one config: issuer 1 solves the base-domain wildcard via DNS-01, issuer 2
  solves custom domains via TLS-ALPN-01 on :443 (no access to the customer's DNS).
  certmagic tries them in order, so a custom domain's failed DNS-01 attempt falls
  through to TLS-ALPN. HTTP-01 is disabled (the relay needs no :80 for ACME).
- 2026-11-09 — (Faz 4) First DNS-01 provider is Cloudflare (libdns) plus a
  "manual TXT" mode (wizard/daemon shows the record and waits). Route53/DO/Hetzner
  deferred to keep dependencies small. User decision.
- 2026-11-09 — (Faz 4) Wizard default ACME environment is Let's Encrypt
  production, with a `--staging` option. User decision.
- 2026-11-09 — (Faz 4) Custom-domain abuse guard: the OnDemand DecisionFunc
  issues a certificate only for (a) names under the base domain (covered by the
  wildcard) or (b) a custom domain that the operator registered AND whose DNS is
  a CNAME to the relay's public host or an A record to its public IP. Unregistered
  or mis-pointed names are denied, so an attacker cannot drive the relay into ACME
  rate limits. Registration lives in a new relay-store `custom_domains` table;
  `IsRegistered` reads it live so CLI changes are seen without a restart.
- 2026-11-09 — (Faz 4) Relay gains an optional YAML config at
  `/etc/latch-relay/config.yaml` (written by `latch-relay install`); `run` reads
  it and CLI flags override. Secrets (Cloudflare token) may instead come from
  `$CLOUDFLARE_API_TOKEN`. The file is 0600.
- 2026-11-09 — (Faz 4) `install` wizard writes config + a hardened systemd unit
  (non-root user, `CAP_NET_BIND_SERVICE` for :80/:443, NoNewPrivileges,
  ProtectSystem=strict) and mints a first token. `deploy/install.sh` downloads the
  binary and runs the wizard. The agent already verifies :7443 against system
  roots (dev CA only when explicitly given), so that Faz 4 deliverable was met in
  Faz 3.
- 2026-11-09 — (Faz 4) The real-VPS DoD (end-to-end ACME issuance, custom-domain
  CNAME) is validated in CI via Pebble (`make test-acme`, build tag `acme_pebble`)
  and on deployment, not in this dev environment. Logic that does not need the
  network (permission function, DNS/clock/port checks, wizard rendering, config
  round-trip) is unit-tested here.

- 2026-11-09 — (Planlama) ROADMAP'e **SaaS Kontrol Düzlemi** diye ayrı bir ürün
  hattı eklendi (Faz S0–S3), self-hosted Faz 0–12'nin üstüne oturur ve onu
  bozmaz. Yeni bileşen `latch-control` (Go + Postgres). Relay'e bayrakla açılan,
  mTLS'li, internete kapalı bir kontrol API'si gelir — "genel yönetim API'si
  yok" kararının SaaS'a özel, kapsamlı gevşetilmesi (self-hosted'da kapalı
  kalır). Bağımlılık: Faz 5, 7, 8. S0 bir onay kapısı; kapatılacak başlıca
  karar: **yönetilen relay** mi **BYO-relay + barındırılan kontrol düzlemi** mi.
  **Karar (2026-11-09): yönetilen relay modeli** seçildi (operatör filoyu
  barındırır). Uygulama sırası: önce self-hosted Faz 5 → 7 → 8, sonra S0–S3.

- 2026-11-09 — (Faz 5) Shared `/policy` package: client-IP resolution (XFF
  trusted only from trusted proxies, walked right-to-left), IP/CIDR + GeoIP
  allow/deny, approximate sliding-window-counter rate limiter (O(1)/key, LRU
  bounded, default 100k keys), header rules, and Evaluate/Decision. Used by the
  relay now and by Caddy in Faz 6.
- 2026-11-09 — (Faz 5) Global/route IP policy combine order (ROADMAP decision
  point resolved as suggested): global deny → route deny → route allow →
  global allow → route default → global default → allow. A policy's rule match
  beats its default; deny at a higher scope wins. Documented in policy/ip.go.
- 2026-11-09 — (Faz 5) GeoIP via maxminddb; relay config `geoip_path`. HelloAck
  advertises CapGeoIP when a DB is loaded. A country rule without a DB does NOT
  deny (fail-open) and the relay logs a warning; the engine reports GeoMissing.
- 2026-11-09 — (Faz 5) Policies persist per token in the relay's SQLite
  (`policies` table) and are enforced after restart and (as a backstop) while
  the agent is offline. The agent also re-sends PolicySync on reconnect.
- 2026-11-09 — (Faz 5) AccessEvents flow relay → agent over the control stream
  as batched `AccessEvent` frames (flush 500ms / 128 events, drop-count under
  load). The agent keeps a 2048-event ring buffer; `latchd events [--follow]`
  reads it (SSE for follow). Durable storage is Faz 7.
- 2026-11-09 — (Faz 5) Relay `/metrics` (Prometheus) on its own listener
  (`--metrics-addr`, default off; NOTES suggests 127.0.0.1:7070): requests by
  decision/status, denials by reason, latency, bytes, agent gauge.
- 2026-11-09 — (Faz 5) Minimal policy CRUD added to the agent API now
  (`/api/ip-policies`, `/api/rate-limits`, route binding fields) so policies can
  exist in the store for PolicySync. Full policy management UI is Faz 9; rate
  limiter memory cap is `DefaultMaxRateKeys` (100k), configurable later.

- 2026-11-09 — (Faz 6) latchd local mode embeds Caddy v2 as a library. Modules
  come from the `standard` bundle plus `filestorage` (the standard bundle omits
  the file-system storage module); the custom `latch_policy` handler
  (`http.handlers.latch_policy`) wraps the shared /policy engine and emits local
  AccessEvents (source=local, where=agent). Config is generated as Caddy JSON
  (deterministic: Go sorts map keys) and applied with `caddy.Load` (zero-downtime;
  Caddy keeps the previous config on error and the daemon never crashes).
- 2026-11-09 — (Faz 6) The latch_policy handler reads a global `Runtime`
  (engine + event sink) that the manager swaps atomically on each config change,
  so Caddy need not re-provision the module when policies change.
- 2026-11-09 — (Faz 6) Caddy only starts once a local route exists, so
  tunnel-only installs never bind :80/:443. Caddy data dir = `<data>/caddy`
  (decision point). HTTP/3 default ON via settings.localMode.http3 (decision
  point) → server protocols h1/h2/h3.
- 2026-11-09 — (Faz 6) The same /policy engine runs in both modes; the relay
  integration test and the local integration test both deny 127.0.0.0/8 → 403
  from one policy table, satisfying the "same policy, same result" DoD.
- 2026-11-09 — (Faz 6) Upstream health checker runs on the agent for both modes
  (TCP connect, or HTTP GET when a path is set); status (healthy/unhealthy/
  unknown) in `/api/status`.
- 2026-11-09 — (Faz 6) `tls: off` (plain-HTTP local routes) is deferred: the
  generator skips them. A local reverse proxy's point is easy HTTPS; mixing a
  plain :80 host with Caddy's auto HTTP->HTTPS redirect server needs more care.
  Backlog.
- 2026-11-09 — (Faz 6) Caddy's own logs are set to ERROR level and are not yet
  routed through slog (they go to stderr). A zapcore->slog bridge is a backlog
  refinement; the agent's own logs already use slog.

- 2026-11-09 — (Faz 7) `latchd/internal/events` is the unified event hub: every
  AccessEvent (local or relay) goes through one `eventSink` into a ring (live
  --follow) + SQLite (query/dashboards). Writes are async-batched; a full queue
  or a DB error drops and counts, never crashes. Retention: age (days) + size
  (MB); over size it deletes the oldest ~5%/pass and WAL-checkpoints. Defaults
  7 days / 500 MB (ROADMAP decision point). Event-store trouble surfaces as a
  synthetic warning in the ring.
- 2026-11-09 — (Faz 7) latchd `/metrics` is served on the management API
  (loopback): requests by source/decision/status, denials by reason, per-route
  traffic, latency histogram, bytes, connected-relays gauge, reconnects, and
  per-route upstream health. `deploy/grafana/` has a scrape config + sample
  dashboard (DoD).
- 2026-11-09 — (Faz 7) Dashboard aggregations are computed server-side from
  SQLite: req/s, p50/p95/p99 (sampled, capped at 100k rows), status classes,
  top routes, currently-blocked (recent denies), top blocked IPs last hour.
  `/api/dashboard?window=`.
- 2026-11-09 — (Faz 7) `policy.Explain(routeID, req)` traces client-IP
  resolution, GeoIP, each IP policy (global then route) with match/result, lists
  rate-limit rules (not executed, to avoid consuming budget), and the final
  decision. `latchd explain` / `POST /api/explain`.
- 2026-11-09 — (Faz 7) Structured logs: slog JSON to a rotated file via
  lumberjack (`--log-file`, 100MB x5, 14d, gzip); stderr text otherwise.
- 2026-11-09 — (Faz 7) High-traffic event sampling (ROADMAP decision point) is
  deferred: size-based retention bounds disk, and the async queue drops under
  burst with a counter. Sampling can be added later. Backlog.

- 2026-11-09 — (Faz 8) Full management API on latchd: config (get/versions/diff/
  rollback/validate), backup (export/import replace|merge), settings, system,
  relay setup-script + dns-check, and a WebSocket `/api/ws` (events + status +
  health + config-version). `POST /api/config/validate` is the dry-run: field
  errors + affected routes, nothing saved.
- 2026-11-09 — (Faz 8) Auth: management password as argon2id (1 pass / 64 MiB /
  4 lanes) in settings.auth.passwordHash; `latchd auth set-password`. Login →
  session cookie (HttpOnly, SameSite=Strict, 24h — decision point) + a CSRF
  token. Mutations require the CSRF token (header) when auth is on; cross-origin
  mutations are always refused via the Origin check (protects loopback too).
  Login is rate-limited to 5/min per IP. The API refuses a non-loopback bind
  unless a password is set (daemon won't start and says why).
- 2026-11-09 — (Faz 8) OpenAPI 3 at docs/openapi.yaml, kept in lockstep with the
  code by TestOpenAPIMatchesRoutes (registered routes == documented paths), which
  is the ROADMAP "OpenAPI matches code (CI check)" DoD. The route table drives
  both registration and the test.
- 2026-11-09 — (Faz 8) WebSocket via github.com/coder/websocket; OriginPatterns
  limited to localhost/127.0.0.1. Settings PUT never returns or clobbers the
  password hash (GET omits it; PUT without it preserves it).

## Backlog

- High-traffic AccessEvent sampling threshold. Faz 7 deferred.

- Local mode `tls: off` (plain-HTTP routes). Faz 6 deferred.
- Bridge Caddy's zap logs into slog (zapcore). Faz 6 deferred.

- gRPC / h2c end-to-end through the tunnel (needs HTTP/2 to the agent). v1+.
- OS keychain for agent-side secrets. v1+.
- GeoIP DB upload from the UI to the relay over the tunnel. v1+.
- UI mobile layout. v1+.
- Multiple sessions of one token load-balancing (currently newest wins). v1+.
