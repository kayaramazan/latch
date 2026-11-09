# Data model

Shared types live in `proto/model` and are used by `latchd`, `latch-relay`,
the API and the UI (as JSON). Field names below are the JSON names
(`camelCase`). Times are RFC 3339 UTC. IDs are prefixed random identifiers
(`rt_…`, `pol_…`, `rl_…`, `rly_…`, 16 bytes base32-lowercase) — never
user-editable.

## Agent-side objects

### Route

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | generated |
| `name` | string | 1–64 chars, unique |
| `enabled` | bool | default `true` |
| `target` | string | `host:port`; host may be IP, name or `localhost`; port 1–65535 |
| `exposure` | `local` \| `tunnel` | |
| `hostnames` | string[] | ≥1 lowercase FQDN, no wildcard. `local`: must not collide with another local route having an overlapping `pathPrefix`. `tunnel`: must be within the relay token's scope (checked by relay, surfaced as sync error) |
| `relayId` | string? | required iff `exposure = tunnel` |
| `kind` | `http` \| `tcp` | default `http`. `tcp` is tunnel-only in v1; `hostnames` empty and `tcpPort` required |
| `tcpPort` | int? | relay-side port requested for TCP tunnels; `0` = relay picks from pool |
| `pathPrefix` | string | default `/`; must start with `/`; longest prefix wins among routes sharing a hostname |
| `tls` | `auto` \| `custom` \| `off` | `tunnel` routes: must be `auto`. `custom` requires `tlsCertFile` and `tlsKeyFile` (local only). `off` serves plain HTTP on `:80` (local only) |
| `tlsCertFile`, `tlsKeyFile` | string? | paths readable by latchd |
| `httpsRedirect` | bool | default `true`; `local` + `tls != off` only |
| `headers` | HeaderRule[] | ≤32 |
| `healthCheck` | HealthCheck | |
| `ipPolicyId` | string? | reference to IPPolicy |
| `rateLimitIds` | string[] | references to RateLimitRule, ≤8 |
| `proxyProtocol` | bool | TCP only: send PROXY v2 header to the local target |
| `createdAt`, `updatedAt` | time | |
| `version` | int | config version in which the route was last changed |

Runtime (not stored, returned by API): `status.health` (`healthy|unhealthy|unknown|disabled`),
`status.tunnel` (`connected|connecting|offline|n/a`), `status.sync` (`synced|pending|error`),
`status.tls` (`active|pending|error`, with message), `publicUrls[]`.

### HeaderRule

`{ "phase": "request"|"response", "op": "set"|"add"|"delete", "name": string, "value": string? }`
— name is a valid header token; `value` required unless `op = delete`.
Hop-by-hop headers cannot be set.

### HealthCheck

`{ "enabled": bool, "path": "/", "intervalSec": 10, "timeoutSec": 3, "expectStatus": [200,399], "unhealthyAfter": 3, "healthyAfter": 1 }`
— `path` only for `http` routes; TCP routes use connect checks.

### IPPolicy

| Field | Type | Rules |
|-------|------|-------|
| `id`, `name` | | |
| `defaultAction` | `allow` \| `deny` | what happens when no rule matches |
| `rules` | IPRule[] | ≤2000, evaluated top-down, first match wins |
| `onDeny` | `{ "mode": "status", "status": 403\|404 }` \| `{ "mode": "drop" }` | `drop` closes the connection without a response |
| `global` | bool | when `true` the policy applies before every route-level policy |

`IPRule`: `{ "action": "allow"|"deny", "cidr": "203.0.113.0/24" }` **or**
`{ "action": …, "country": "TR" }` (ISO 3166-1 alpha-2). Exactly one matcher per rule.
Single IPs are stored as `/32` or `/128`.

Evaluation order for a request: global policies (deny wins) → route policy →
default allow. A route's `onDeny` is used when its own policy denies, a global
policy's `onDeny` otherwise.

### RateLimitRule

| Field | Type | Rules |
|-------|------|-------|
| `id`, `name` | | |
| `keys` | string[] | 1–3 of: `ip`, `path`, `header:<Name>`; combined with `\|` into one bucket key |
| `limit` | int | ≥1 |
| `windowSec` | int | 1–3600; sliding window |
| `onExceed` | `{ "mode": "reject" }` (429 + `Retry-After`) \| `{ "mode": "delay", "maxDelayMs": 2000 }` | |
| `global` | bool | applies to every route in addition to route-level rules |

A request must pass every attached rule. Limits are per enforcement point
(relay or agent) and in-memory; they are not shared across relays (v1).

### Relay

| Field | Type | Rules |
|-------|------|-------|
| `id`, `name` | | |
| `host` | string | relay FQDN |
| `port` | int | default 7443 |
| `token` | string | plain agent token (stored 0600; shown masked by API) |
| `caPem` | string? | extra CA to trust (dev); system roots always used |
| `baseDomain` | string | e.g. `relay.example.com`; used to generate subdomains |

Runtime: `status.connection` (`connected|connecting|backoff|stopped`), `status.lastError`,
`status.reconnects`, `status.policyVersionAcked`, `status.sync` (`synced|out_of_sync`),
`status.relayVersion`, `status.protocolVersion`, `scope` (hostnames granted by the token).

### Settings (single object)

```
{
  "listen": "127.0.0.1:4040",
  "auth": { "mode": "none"|"password", "passwordHash": "…" },   // "none" only valid on loopback
  "trustedProxies": ["10.0.0.0/8"],                              // XFF accepted from these (local mode)
  "localMode": { "httpPort": 80, "httpsPort": 443, "http3": true, "acmeEmail": "", "acmeCa": "letsencrypt"|"letsencrypt-staging"|"zerossl" },
  "geoip": { "dbPath": "" },
  "events": { "retentionDays": 7, "maxSizeMb": 500 },
  "log": { "level": "info", "format": "json"|"text" },
  "language": "en"
}
```

### ConfigSnapshot and ConfigVersion

A snapshot is the entire configuration:

```
{ "routes": [...], "ipPolicies": [...], "rateLimits": [...], "relays": [...], "settings": {...} }
```

`config_versions(version INTEGER PK, created_at, author, summary, snapshot JSON)`.
Every mutation writes version N+1 with the full snapshot (small data, simple
diffing). `Rollback(n)` validates snapshot `n` and writes it as a new version
with summary `rollback to n`. At most 200 versions are kept by default (oldest pruned; `store.Options.MaxVersions`).

Defaults are applied by `Normalize()`; boolean fields cannot be defaulted that
way, so new objects are built with `NewRoute`, `NewRelay`, `NewIPPolicy`,
`NewRateLimitRule` and `DefaultSettings`, and partial updates must merge onto
the existing object.

Validation runs on the **whole snapshot**, so cross-object rules (dangling
`ipPolicyId`, hostname collisions, relay existence) are caught before writing.
Errors are `FieldError{ path: "routes[2].hostnames[0]", code: "invalid_hostname", message: "…" }`.

`Diff(a, b)` returns added/removed/changed objects and the set of **affected
routes** (a route is affected if it changed, or a policy/rate limit/relay it
references changed, or a global policy changed). The API's dry-run returns
exactly this.

## Relay-side objects (relay SQLite)

| Table | Fields |
|-------|--------|
| `tokens` | `id, name, hash (argon2id), scope JSON {hostnames[], tcpPorts: bool}, created_at, expires_at?, revoked_at?, last_seen_at?` |
| `routes` | `hostname PK, token_id, route_id (agent's), kind, tcp_port?, announced_at, last_online_at` — kept while offline |
| `policy_snapshots` | `token_id PK, version, snapshot JSON (ipPolicies, rateLimits, routes' policy bindings, trustedProxies n/a)` |
| `custom_domains` | `hostname PK, token_id, verified_at?, last_check_at, last_error?` |
| `settings` | relay's own config mirror (read from `/etc/latch-relay/config.yaml`) |

## Events

`AccessEvent` (both modes, stored in `latchd` `events` table and emitted on WS):

```
{ "ts", "source": "local"|"relay", "relayId"?, "routeId", "hostname", "clientIp", "country"?,
  "method", "path", "status", "durationMs", "bytesIn", "bytesOut", "target",
  "decision": "allow"|"deny", "denyReason"?: "ip_policy"|"geo"|"rate_limit"|"agent_offline"|"unknown_host",
  "ruleId"?, "where": "relay"|"agent" }
```

`SystemEvent`: `{ "ts", "level", "kind": "tunnel_connected"|"tunnel_lost"|"config_applied"|"config_failed"|"cert_issued"|"cert_error"|"policy_synced"|"policy_sync_failed"|"disk_pressure"|…, "message", "data" }`.

Both are retained per `settings.events`; when the size cap is hit the oldest
rows are deleted in batches and a `disk_pressure` system event is written. If
the disk is full and even deletion fails, event writes are dropped (counted in
`/metrics`) and the daemon keeps proxying.
