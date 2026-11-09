# Latch managed SaaS — data model (S0 design gate)

PostgreSQL schema for `latch-control`. SQLite (used by the self-hosted relay and
agent) does not fit here: multi-tenant, HA, concurrent writers, reporting. This
is a design sketch; S2 implements it with migrations.

Conventions: `id` is a ULID/UUID string; timestamps are `timestamptz`; money is
integer minor units; every tenant-owned row carries `account_id` and every query
is scoped by it.

## Tenancy & identity

```
account
  id, name, status(active|suspended|closed), plan_id,
  mor_customer_id,            -- Merchant-of-Record customer handle
  region (default 'eu'),
  created_at, suspended_at, suspended_reason

user
  id, account_id, email (citext, unique), email_verified_at,
  password_hash (argon2id), role(owner|admin|member),
  totp_secret (nullable, encrypted), totp_enabled_at,
  created_at, last_login_at

session   id, user_id, created_at, expires_at, csrf, ip, user_agent
api_key   id, account_id, name, hash (argon2id), scopes, created_at, last_used_at, revoked_at
invite    id, account_id, email, role, token_hash, expires_at, accepted_at
```

Roles: **owner** (billing + delete account), **admin** (manage everything),
**member** (manage routes/tokens, no billing).

## Projects, tokens, domains (tenant resources)

```
project       id, account_id, name, created_at

tunnel_token  id, account_id, project_id, relay_id, name,
              scope_json,            -- hostnames / tcp, same shape as tunnel.Scope
              status(active|revoked), created_at, last_seen_at
              -- the SECRET is never stored here; the relay keeps the argon2id
              --   hash. The control plane stores metadata and issued it once.

custom_domain id, account_id, domain (unique), relay_id,
              verified_at, status(pending|verified|failed), created_at

route_meta    id, account_id, token_id, route_id, hostname, kind, created_at
              -- optional mirror of agent-announced routes for the dashboard;
              --   source of truth stays on the relay/agent.
```

## Fleet (operator-owned)

```
relay
  id, region, hostname, base_domain (e.g. eu.latch.app),
  control_addr,              -- private mTLS endpoint
  capacity_tokens, capacity_conns,
  version, status(active|draining|down), last_heartbeat_at, health_json

placement
  account_id, relay_id, assigned_at          -- one active relay per account (v1)
```

## Plans, quotas, metering, billing

```
plan
  id, name(free|pro|team), price_cents, currency,
  max_tokens, max_custom_domains, max_concurrent_tunnels,
  bandwidth_gb_month, requests_per_min, overage_cents_per_gb,
  features_json

usage_rollup                               -- aggregated from relay reports
  account_id, period (date, daily),
  bytes_in, bytes_out, connections, max_concurrent, requests, denials
  PRIMARY KEY (account_id, period)

usage_event_raw                            -- short-retention landing table
  id, account_id, relay_id, ts, bytes_in, bytes_out, connections, routes

subscription   id, account_id, mor_subscription_id, plan_id,
               status(trialing|active|past_due|canceled), current_period_end
billing_event  id, account_id, mor_event_id (unique), type, payload_json, received_at
```

Quota enforcement is two-tier: the **relay** enforces per-token limits (exists:
sessions, routes, rate limits) and the **control plane** enforces per-account
aggregates (tokens, domains, concurrent tunnels, monthly bandwidth) from
`usage_rollup`, soft (warn) then hard (suspend/throttle).

## Abuse & audit

```
abuse_report   id, account_id (nullable), domain, reporter, kind(phishing|malware|dmca|other),
               body, status(open|actioned|dismissed), created_at, actioned_at
blocklist      id, pattern (domain/ip/cidr), reason, created_at
audit_log      id, account_id (nullable), actor_user_id (nullable), actor('system'|user|'mor'),
               action, target_type, target_id, metadata_json, ip, created_at
```

## GDPR / KVKK

- **Export**: a per-account dump (account, users sans secrets, projects, tokens
  metadata, domains, usage_rollup, audit_log) as JSON.
- **Delete**: account close → revoke all tokens/domains on relays, delete tenant
  rows, retain minimal billing/audit records for the legal period, then purge.
- `usage_event_raw` is short-retention (days); `usage_rollup` is aggregate and
  carries no request contents. No request bodies are ever stored.

## Isolation

Logical isolation by `account_id` on every tenant row, enforced in the control
plane's data-access layer (and optionally PostgreSQL row-level security). The
relay additionally separates tenants by token. v1 does not do per-tenant
database/relay separation (that is the multi-region/residency path, deferred).
