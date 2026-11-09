# Management API (latchd)

Base URL: `http://127.0.0.1:4040/api/v1`. JSON everywhere. The same API is
used by the web UI, the `latchd` CLI and Electron. An OpenAPI 3 document
(`docs/openapi.yaml`) is generated from the handlers in phase 8 and checked in
CI.

Phases: a minimal subset (`routes`, `relays`, `status`) ships in phase 3
without auth on loopback; the full surface and auth ship in phase 8.

## Conventions

- Mutations: `POST` create, `PUT` full replace, `PATCH` partial, `DELETE`.
- Every mutation returns `{ "data": <object>, "configVersion": 42, "affectedRoutes": ["rt_…"] }`.
- Errors:

```json
{ "error": { "code": "validation_failed", "message": "…",
             "fields": [ { "path": "routes[0].target", "code": "invalid_port", "message": "…" } ] } }
```

  Codes: `unauthorized`, `forbidden`, `not_found`, `validation_failed`,
  `conflict`, `apply_failed` (Caddy/relay rejected; previous config kept),
  `rate_limited`, `internal`.
- Lists: `?limit=` (default 50, max 500) and `?cursor=`; response
  `{ "data": [...], "nextCursor": "…" }`.
- Secrets (`relays[].token`, `passwordHash`) are never returned; `token` is
  returned as `"…abcd"` (last 4).

## Authentication

| Method | Path | Notes |
|--------|------|-------|
| `POST` | `/auth/login` | `{ "password": "…" }` → sets `latch_session` cookie (HttpOnly, SameSite=Strict, Secure when TLS). Rate limit 5/min/IP. |
| `POST` | `/auth/logout` | |
| `GET`  | `/auth/me` | `{ "authenticated": true, "mode": "none"\|"password", "csrfToken": "…" }` |

- `settings.auth.mode = none` is allowed only when `listen` is loopback; the
  daemon refuses to start otherwise and logs the fix.
- Mutating requests must send `X-Latch-CSRF: <csrfToken>` when a session
  cookie is used. Bearer tokens (`Authorization: Bearer <api token>`, created
  via `POST /auth/tokens`) skip CSRF — used by the CLI.

## Resources

### Routes

| Method | Path |
|--------|------|
| `GET` | `/routes` — includes runtime `status` and `publicUrls` |
| `POST` | `/routes` |
| `GET` / `PUT` / `PATCH` / `DELETE` | `/routes/{id}` |
| `POST` | `/routes/{id}/enable`, `/routes/{id}/disable` |
| `POST` | `/routes/{id}/health-check` — run a probe now |
| `POST` | `/routes/suggest-hostname` — `{ "relayId": "…", "hint": "myapp" }` → `{ "hostname": "myapp-7f3k.relay.example.com" }` |

### IP policies and rate limits

| Method | Path |
|--------|------|
| `GET` / `POST` | `/ip-policies` |
| `GET` / `PUT` / `DELETE` | `/ip-policies/{id}` |
| `POST` | `/ip-policies/{id}/test` — `{ "ip": "…" }` → `{ "decision": "allow"\|"deny", "rule": …, "country": "TR" }` |
| `GET` / `POST` | `/rate-limits` |
| `GET` / `PUT` / `DELETE` | `/rate-limits/{id}` |
| `GET` | `/rate-limits/{id}/blocked` — currently limited keys (agent-side) plus relay-reported ones |

### Relays

| Method | Path |
|--------|------|
| `GET` / `POST` | `/relays` — `POST` accepts `{ "connectionString": "latch://…" }` or explicit fields |
| `GET` / `PUT` / `DELETE` | `/relays/{id}` |
| `POST` | `/relays/{id}/reconnect` |
| `POST` | `/relays/{id}/resync` — force full RouteAnnounce + PolicySync |
| `POST` | `/relays/setup-script` — `{ "domain": "relay.example.com", "email": "…", "dnsProvider": "cloudflare"\|…\|"manual" }` → `{ "command": "curl -fsSL … \| sh -s -- …", "dnsRecords": [...] }` |
| `POST` | `/relays/dns-check` — `{ "domain": "…", "expectedIp": "…" }` → `{ "ok": bool, "records": [...], "hint": "…" }` |

### Configuration

| Method | Path |
|--------|------|
| `POST` | `/config/validate` — body: partial or full snapshot; returns `fields[]` and `affectedRoutes[]` without writing |
| `GET` | `/config/versions` — list `{ version, createdAt, author, summary }` |
| `GET` | `/config/versions/{n}` — snapshot |
| `GET` | `/config/versions/{a}/diff/{b}` |
| `POST` | `/config/versions/{n}/rollback` |
| `GET` | `/config/export` — full snapshot incl. secrets, `Content-Disposition: attachment` (backup) |
| `POST` | `/config/import` — `{ "snapshot": {...}, "mode": "replace"\|"merge" }`, validated, new version |

### Events, logs, stats

| Method | Path |
|--------|------|
| `GET` | `/events` — filters: `from`, `to`, `routeId`, `source`, `ip`, `status`, `decision`, `denyReason`, `q` (substring on path) |
| `GET` | `/events/{id}` |
| `GET` | `/logs` — system log lines; filters `from`, `to`, `level`, `q` |
| `GET` | `/stats/overview?window=1h` — req/s series, p50/p95/p99, status distribution, bytes |
| `GET` | `/stats/routes?window=1h` — per-route traffic and error rate |
| `GET` | `/stats/blocked?window=1h` — top blocked IPs/keys with reasons |
| `GET` | `/stats/tunnels` — per relay: state, uptime, reconnects, rtt |
| `POST` | `/explain` — `{ "routeId": "…", "clientIp": "…", "path": "/", "headers": {...} }` → ordered `[ { "where": "relay"\|"agent", "stage": "ip_policy"\|"geo"\|"rate_limit"\|"headers", "ruleId", "result", "note" } ]` |

`GET /metrics` (Prometheus, outside `/api`) is served on the same listener.

### Settings and system

| Method | Path |
|--------|------|
| `GET` / `PUT` | `/settings` |
| `POST` | `/settings/password` — `{ "current": "…", "new": "…" }` |
| `POST` | `/settings/geoip` — multipart upload of an `.mmdb` file |
| `GET` | `/system` — `{ version, protocolVersion, uptime, dataDir, listen, localModeActive, platform }` |
| `GET` | `/system/health` — `200` when the daemon is serving; unauthenticated |
| `POST` | `/system/restart-local-proxy` |
| `GET` | `/i18n/{lang}.json` — UI translations (served from the embedded bundle) |

## WebSocket `/api/v1/ws`

Authenticated like REST (cookie or `?token=`). Server → client frames:

```json
{ "type": "access_event", "data": { …AccessEvent… } }
{ "type": "system_event", "data": { …SystemEvent… } }
{ "type": "route_status", "data": { "routeId": "…", "status": { … } } }
{ "type": "relay_status", "data": { "relayId": "…", "status": { … } } }
{ "type": "config_version", "data": { "version": 43, "summary": "…" } }
{ "type": "stats_tick", "data": { "reqPerSec": 12.4, "p95Ms": 83, … } }   // every 2s
```

Client → server: `{ "type": "subscribe", "channels": ["access_event", ...], "filter": { "routeId": "…" } }`.
Access events are rate-limited to 200/s per socket; beyond that the server
sends `{ "type": "dropped", "count": n }`.

## CLI mapping

`latchd` subcommands are thin wrappers: `route add|list|rm|enable|disable`,
`relay add|list|rm`, `policy …`, `ratelimit …`, `events --follow`, `explain`,
`config versions|rollback|export|import`, `auth set-password|token create`,
`status`, `run`, `install|uninstall`, `version`.
