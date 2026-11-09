# Local development

This shows the phase-2 relay working end to end with a development CA. The
agent (`latchd`) tunnel client lands in phase 3; until then a small test
agent inside the integration tests plays that role, and this guide uses
`curl` against the relay.

## Prerequisites

- Go 1.24+
- `make build` produces `bin/latchd` and `bin/latch-relay`.

## Run a relay with development certificates

```sh
# 1. Start a relay for the wildcard zone *.relay.local using a throwaway CA.
bin/latch-relay run --dev \
  --public-host relay.local \
  --base-domain relay.local \
  --https-addr :8443 --http-addr :8080 --agent-addr :7443 \
  --data-dir /tmp/latch-relay --log-level debug
```

The relay creates `/tmp/latch-relay/devca/` with `ca.pem` (trust this),
`server.pem` and `server.key`. It listens on:

- `:8443` public HTTPS (the wildcard certificate covers `*.relay.local`),
- `:8080` HTTP → HTTPS redirect,
- `:7443` agent tunnel port (TLS, ALPN `latch/1`).

## Issue an agent token

In another terminal:

```sh
bin/latch-relay token create --name laptop \
  --hostnames '*.relay.local' --tcp \
  --data-dir /tmp/latch-relay --dev
```

This prints a connection string that embeds the dev CA, e.g.

```
latch://relay.local:7443/?token=lt_...&ca=...&name=laptop
```

In phase 3 you paste that into `latchd`. The token is shown once; only its
argon2id hash is stored.

## See the relay answer before an agent connects

Point `relay.local` and a subdomain at loopback for the test:

```sh
echo "127.0.0.1 relay.local app.relay.local" | sudo tee -a /etc/hosts
```

```sh
# Unknown/offline hostname → the relay's 404/503 page, served with valid TLS.
curl --cacert /tmp/latch-relay/devca/ca.pem https://app.relay.local:8443/
# HTTP is redirected to HTTPS:
curl -i --cacert /tmp/latch-relay/devca/ca.pem http://app.relay.local:8080/
```

Certificate verification is never disabled: `curl` trusts the dev CA via
`--cacert`, exactly as the agent will via the `ca=` parameter.

## Tokens

```sh
bin/latch-relay token list   --data-dir /tmp/latch-relay
bin/latch-relay token revoke <id> --data-dir /tmp/latch-relay
```

A running relay disconnects a revoked token's agents within 30 seconds and
withdraws their routes.

## What the integration tests cover

`go test ./latch-relay/internal/relay/` starts a relay on random ports with a
dev CA and a minimal in-process agent, then checks: HTTP request end to end
(with `X-Forwarded-For/-Proto`), WebSocket passthrough, a TCP tunnel,
token-scope and hostname-conflict rejections, supersede and revoke, the
HTTP→HTTPS redirect, the agent-port ALPN requirement, and that announced
routes survive a relay restart as offline entries.

## Phase 3 — expose a local port through the relay (end to end)

With a relay running on the dev CA (above), run the agent and point a local
port at a public hostname. This mirrors the automated end-to-end test, but by
hand.

```sh
go build -o bin/latchd ./latchd

# 1. A local service to expose.
python3 -m http.server 3000        # or any app on 127.0.0.1:3000

# 2. A token scoped to the zone, with the dev CA embedded in the conn string.
bin/latch-relay token create --data-dir /tmp/latch-relay \
  --name laptop --hostnames '*.relay.local' --host 127.0.0.1 --port 7443 --dev
# → prints: latch://127.0.0.1:7443/?token=lt_...&ca=...&name=laptop

# 3. Start the agent (management API on 127.0.0.1:4040).
bin/latchd run --data-dir /tmp/latchd &

# 4. Register the relay and a route (CLI talks to the agent's local API).
bin/latchd relay add --url 'latch://127.0.0.1:7443/?token=lt_...&ca=...&name=laptop'
bin/latchd route add --target 127.0.0.1:3000 --tunnel laptop --hostname app.relay.local
bin/latchd status

# 5. Reach the local service through the relay over HTTPS.
curl --cacert /tmp/latch-relay/devca/ca.pem \
  --resolve app.relay.local:8443:127.0.0.1 https://app.relay.local:8443/
```

`latchd status` shows each relay's state (`connecting`/`connected`/`backoff`/
`stopped`), RTT, announced routes and public URLs.

Kill the agent (`kill %1` or Ctrl-C) and the hostname returns the relay's
`503 Agent offline` page while the route stays registered. Start `latchd run`
again and the tunnel re-establishes itself within a second or two — no manual
re-announce. On a `*.relay.local` demo without real DNS, add
`127.0.0.1 app.relay.local` to `/etc/hosts` (or keep using `curl --resolve`).

The management API is loopback-only and unauthenticated in this phase; it
refuses to bind a non-loopback address until authentication lands in phase 8.

## What the agent end-to-end tests cover

`go test ./latchd/internal/tunnel/` runs a relay, the real agent client and a
local HTTP server in one process and checks: an HTTPS request reaching the
local target with the client IP in `X-Forwarded-For`, WebSocket passthrough, a
down local target surfacing as `502`, automatic reconnection after a relay
outage (route works again in well under 10 s), and a protocol-version mismatch
reported as a clear error. `go test ./latchd/internal/api/` covers the
management API (route/relay lifecycle, structured validation errors,
loopback-only binding).

## Phase 4 — real certificates, setup wizard, custom domains

On a public server the relay obtains its own certificates with ACME instead of
the dev CA.

### Setup wizard

```sh
# One-line install (downloads the binary, runs the wizard):
curl -sSL https://github.com/kayaramazan/latch/releases/latest/download/install.sh | sh

# Or, with a local build, run the wizard directly:
bin/latch-relay install            # interactive
bin/latch-relay install --yes \
  --public-host relay.example.com --email you@example.com \
  --public-ip 203.0.113.10 --dns cloudflare --cloudflare-token "$CF_TOKEN" \
  --dry-run                        # prints the config + systemd unit, writes nothing
```

The wizard writes `/etc/latch-relay/config.yaml` (0600), a hardened systemd unit
(`/etc/systemd/system/latch-relay.service`, non-root + `CAP_NET_BIND_SERVICE`),
mints a first token and prints a `latch://…` connection string. It runs a DNS
precheck that warns if `*.<base-domain>` does not resolve to the server yet.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now latch-relay
latch-relay doctor --config /etc/latch-relay/config.yaml
```

### ACME modes

- **Cloudflare DNS-01** (wildcard): set `acme.dns_provider: cloudflare` and a
  scoped `cloudflare_token` (or `$CLOUDFLARE_API_TOKEN`). The relay gets
  `*.relay.example.com` automatically.
- **Manual TXT**: `acme.dns_provider: manual`. The relay logs the `_acme-challenge`
  TXT record to create; add it, and the certificate is issued on the next attempt.
- **Staging**: `acme.staging: true` (or `--acme-staging`) uses Let's Encrypt
  staging — untrusted certs, but high rate limits for testing.

Run the relay from the config:

```sh
latch-relay run --config /etc/latch-relay/config.yaml
```

### Custom domains

A customer CNAMEs their hostname to the relay; the operator registers it:

```sh
latch-relay domain add app.customer.com
# customer adds: app.customer.com CNAME relay.example.com
latch-relay domain verify app.customer.com     # checks DNS points here
latch-relay domain list
```

Only registered domains whose DNS actually points at the relay get an on-demand
certificate (TLS-ALPN-01). This is the ACME abuse guard: the relay never
requests a certificate for a name it was not told to serve and that has not
delegated to it. An agent announcing a route for the custom hostname then serves
it like any other.

### Agent trust

The agent verifies the relay's `:7443` certificate against the system roots. A
dev CA is trusted only when it is explicitly provided (the `ca=` connection-string
parameter or `--ca-file`); verification is never skipped.

### ACME integration test (Pebble)

The ACME issuance path is exercised against a local
[Pebble](https://github.com/letsencrypt/pebble) server, not real Let's Encrypt:

```sh
# with Pebble + challtestsrv running:
PEBBLE_CA_FILE=/path/to/pebble.minica.pem make test-acme
```

This test is behind the `acme_pebble` build tag and excluded from `make test`;
CI runs it as a separate job.

## Phase 5 — relay policies and the event stream

The relay enforces access policies (IP/CIDR, country, rate limit, headers)
*before* a request reaches the agent, and streams every decision back to the
agent so you can watch traffic with `latchd events`.

### Set a policy and watch it work

With a tunnel up (phase 3 walkthrough), create a policy on the agent and bind
it to a route, then watch the relay enforce it:

```sh
# Block a CIDR and bind the policy to a route.
POL=$(curl -s -X POST http://127.0.0.1:4040/api/ip-policies \
  -d '{"name":"block","rules":[{"action":"deny","cidr":"203.0.113.0/24"}]}' | jq -r .id)
curl -s -X PUT http://127.0.0.1:4040/api/routes/<route-id> -d "{\"ipPolicyId\":\"$POL\"}"

# A rate limit (2 requests / 60s per IP):
RL=$(curl -s -X POST http://127.0.0.1:4040/api/rate-limits \
  -d '{"name":"slow","keys":["ip"],"limit":2,"windowSec":60}' | jq -r .id)
curl -s -X PUT http://127.0.0.1:4040/api/routes/<route-id> -d "{\"rateLimitIds\":[\"$RL\"]}"

# Watch decisions stream from the relay:
latchd events --follow
# 2026-… app.relay.local  203.0.113.9  GET /  -> 403  DENY(ip_policy)
```

Denied requests are cut at the relay (the agent is never dialed), the agent
still sees the event, and `latchd status` shows each relay's `syncState`
(`in_sync`/`out_of_sync`) so you know the relay has your current policy.

### GeoIP

Country rules (`{"action":"deny","country":"RU"}`) need a MaxMind database on
the relay: `latch-relay run --geoip /path/GeoLite2-Country.mmdb` (or `geoip_path`
in the config). Without it the relay cannot match country rules — it fails open
and logs a warning, and `HelloAck` tells the agent GeoIP is unavailable.

### Metrics

```sh
latch-relay run --dev … --metrics-addr 127.0.0.1:7070
curl -s http://127.0.0.1:7070/metrics | grep latch_relay_
# latch_relay_requests_total, latch_relay_denied_total{reason="ip_policy"}, …
```

### What the policy tests cover

`go test ./policy/` table-tests client-IP/XFF trust, IP/CIDR allow-deny and
allowlists, the global-vs-route precedence, the sliding window, and header
rules. `go test ./latch-relay/internal/policies/` covers persistence across a
restart. `go test ./latch-relay/internal/relay/ -run TestPolicy` is the
integration test: a denied IP gets 403 and never reaches the agent, the agent
receives the AccessEvent, and a rate limit returns 429 with Retry-After.

## Phase 6 — local mode (embedded Caddy)

The same `latchd` binary can run on a public-IP server as a TLS reverse proxy,
with no Caddy config to write. Local routes are served by an embedded Caddy and
go through the same `/policy` engine as tunnel routes.

```sh
# Expose a local service with automatic HTTPS (Let's Encrypt on a real server):
latchd route add --local --hostname site.example.com --target 127.0.0.1:8080
latchd route list        # EXPOSURE=local
```

Caddy starts only once a local route exists (tunnel-only installs bind no extra
ports). The data/cert directory is `<data-dir>/caddy`. HTTP/3 is on by default
(`settings.localMode.http3`). `latchd status`/`/api/status` reports upstream
health for routes with health checks enabled.

Policies work identically in both modes — bind an IP policy or rate limit to a
local route exactly as for a tunnel route, and `latchd events` shows local
decisions (`where=agent`, `source=local`).

### What the local-mode tests cover

`go test ./latchd/internal/local/` runs: generator structure/golden tests (the
Caddy JSON for a route has the right host, upstream, `latch_policy` handler,
issuer, path matcher and health check), and an end-to-end integration test that
embeds Caddy with its **internal CA**, serves a local upstream over real HTTPS
(the test trusts the internal CA — verification is never skipped), applies a
response-header rule, then binds an IP policy and confirms the request is denied
with a 403 and a local AccessEvent. The `tls: off` (plain-HTTP) mode is deferred
(see NOTES).

## Phase 7 — observability

Every request, local or relay, lands in one event store and is visible in one
stream, tagged with its source.

```sh
latchd events --follow                 # live, all routes
latchd events --route rt_… -n 100      # recent for one route
latchd events --follow | grep DENY     # watch blocks
```

Each line is `time [source] host client-ip method path -> status decision`,
where `source` is `relay` (tunnel mode) or `local` (embedded Caddy).

### Explain

Ask why a request would be allowed or denied, without sending it:

```sh
latchd explain --route rt_… --ip 203.0.113.9 --path /admin --header "X-Env: prod"
#   * client_ip   resolved to 203.0.113.9 (trusted proxies: [])
#   * ip_policy:route  route policy pol_… matched → deny
#   => DENY (ip_policy, rule pol_…)
```

### Dashboard and metrics

```sh
curl -s http://127.0.0.1:4040/api/dashboard?window=3600
# { requests, requestsPerSec, denied, p50Ms, p95Ms, p99Ms, statusClasses,
#   topRoutes, currentlyBlocked, topBlockedIpsLastHour }

curl -s http://127.0.0.1:4040/metrics | grep latchd_
# latchd_requests_total, latchd_denied_total{reason=…}, latchd_tunnels_connected, …
```

Scrape both the agent and the relay with the example config and import the
sample dashboard:

```sh
deploy/grafana/prometheus.yml        # scrape 127.0.0.1:4040 (agent) + :7070 (relay)
deploy/grafana/latch-dashboard.json  # import into Grafana
```

### Retention and logs

Events are kept for `settings.events.retentionDays` (default 7) and capped at
`settings.events.maxSizeMB` (default 500); the oldest are dropped first and a
full queue or disk problem is logged, never fatal. Structured JSON logs go to a
rotated file with `--log-file /var/log/latchd.log`.

### What the observability tests cover

`go test ./latchd/internal/events/` covers append/ring/recent, query filters,
route-filtered subscriptions, aggregation (counts, status classes, top routes,
top blocked IPs), size-based retention (40k events trimmed under a 1 MB cap),
and percentiles. `go test ./policy/ -run TestExplain` covers the explain trace.

## Phase 8 — management API and authentication

The agent's API is complete and documented in `docs/openapi.yaml`. A test keeps
the spec and the code in sync (it fails if a route is added without documenting
it, or vice versa) — that is the CI check for "OpenAPI matches code".

### Authentication

Loopback is open by default. To protect the API (required before binding a
non-loopback address):

```sh
latchd auth set-password            # prompts, or --password
latchd run --listen 0.0.0.0:4040    # refuses to start without a password
```

With a password set, clients log in and carry a session cookie plus a CSRF token:

```sh
# login → Set-Cookie: latch_session=…  and {"csrfToken":"…"}
curl -c jar -X POST localhost:4040/api/auth/login -d '{"password":"…"}'
# reads need the cookie:
curl -b jar localhost:4040/api/routes
# mutations also need the CSRF header:
curl -b jar -H "X-CSRF-Token: <token>" -X POST localhost:4040/api/ip-policies -d '{…}'
```

Cross-origin mutations are refused (the `Origin` check), so a malicious website
cannot drive the loopback API even when auth is off. Logins are rate-limited to
5/minute per IP.

### Config, validation, backup

```sh
curl localhost:4040/api/config/versions          # history
curl -X POST localhost:4040/api/config/rollback/3 # roll back
# dry-run a proposed snapshot before saving (field errors + affected routes):
curl -X POST localhost:4040/api/config/validate -d @snapshot.json
curl localhost:4040/api/backup > backup.json      # export
curl -X POST 'localhost:4040/api/backup?mode=replace' -d @backup.json
```

### WebSocket

`GET /api/ws` streams `{type: event|status|health|config}` frames — the UI uses
it for live updates instead of polling.

### What the API tests cover

`go test ./latchd/internal/api/` covers every resource over httptest, the auth
flow (401 → login → CSRF-gated mutations), the cross-origin CSRF defense, the
non-loopback-requires-password rule, config validate/rollback, backup
export/import, the WebSocket handshake, and `TestOpenAPIMatchesRoutes` (spec ==
code).

## Phase 9 — the web UI

`latchd` serves a React single-page app (latch-ui) at the same origin as the
API, so the whole product is usable from a browser with no terminal.

### Develop

```sh
cd latch-ui
pnpm install
pnpm dev          # Vite dev server on :5173, proxying /api to 127.0.0.1:4040
pnpm test         # Vitest component tests
pnpm check-i18n   # fails if en/tr dictionaries diverge
```

### Build and embed

```sh
make ui-build     # writes latchd/internal/web/dist (embedded via go:embed)
make build-latchd # the binary now serves the UI
latchd run        # open http://127.0.0.1:4040
```

The built bundle is committed so `go build` always yields a working binary;
regenerate it with `make ui-build`. The management API gives `/api` and
`/metrics` precedence; everything else is served by the SPA (unknown paths fall
back to `index.html` for client-side routing). When a management password is
set, the UI itself always loads (so the login screen appears), but API calls
require the session cookie and a CSRF token.

### Screens

Dashboard, Routes (+ two-step add wizard), Route detail, IP policies (with a
"does this IP pass?" tester backed by `/api/explain`), Rate limits, Relays
(connect by pasting a `latch://` string, "set up your relay" wizard, live sync
state), Live (SSE event stream), History (versions + rollback + backup), Settings
and Explain. Every string goes through i18n (English default, Turkish included).

### Tests

`pnpm test` (Vitest) covers the add-route wizard — the critical form. `make
test-ui-e2e` runs the Playwright flow (`e2e/add-route.spec.ts`) end to end
against a running latchd; it needs a browser and runs in CI, not in this dev
environment. The i18n completeness check (`make test-ui`) is the CI gate for the
"missing translation key fails the build" requirement.

## SaaS S1 — the relay control API and latch-control

The managed SaaS adds a private, mTLS control API to the relay and a skeleton
control plane (`latch-control`). This is **off for self-hosted** relays (no
`--control` flag → nothing changes).

### Bootstrap the dev mTLS trust set

```sh
latch-control gen-certs --dir ./certs --relay-hosts 127.0.0.1,localhost
# writes ca.crt/key, relay-control.crt/key (server), control-plane.crt/key (client)
```

### Run a relay with the control API + heartbeats

```sh
latch-relay run --dev --public-host 127.0.0.1 --base-domain relay.local \
  --agent-addr 127.0.0.1:7643 --https-addr 127.0.0.1:8643 --http-addr "" \
  --control --control-addr 127.0.0.1:7644 \
  --control-cert certs/relay-control.crt --control-key certs/relay-control.key \
  --control-client-ca certs/ca.crt \
  --relay-id relay-eu-1 --region eu --public-ip 127.0.0.1 \
  --control-plane-url http://127.0.0.1:9100 --control-reg-token SECRET
```

### Run the control plane and drive the relay

```sh
latch-control serve --listen 127.0.0.1:9100 --reg-token SECRET \
  --client-cert certs/control-plane.crt --client-key certs/control-plane.key --relay-ca certs/ca.crt

curl 127.0.0.1:9100/relays                              # fleet inventory + usage (from heartbeats)
curl -X POST 127.0.0.1:9100/relays/relay-eu-1/tokens \  # mint a token on the relay over mTLS
  -d '{"name":"account-123","hostnames":["*.relay.local"]}'
# → {"connectionString":"latch://…"} — paste into an agent
```

The relay's control API is only reachable with the control plane's client
certificate; a plain request fails at the TLS handshake. The relay heartbeats
its usage (bytes, requests, denials) and health (agents, routes) every 15s, so
`GET /relays` reflects live traffic.

### What the S1 tests cover

`go test ./latch-relay/internal/control/` exercises the mTLS enforcement (no
client cert → rejected) and the token/domain lifecycle through the shared
`proto/control` client. `go test ./latch-control/internal/plane/` covers
heartbeat auth + fleet inventory. The full chain (control-plane token → agent
tunnel → usage in the control plane) was verified by hand.

## SaaS S2 — accounts, multi-tenancy, self-service

`latch-control` is now a multi-tenant control plane: customers sign up and
manage their own tokens and domains; the control plane places them on the relay
fleet. Self-hosted relays are unaffected.

### Run it (dev, in-memory)

```sh
latch-control gen-certs --dir ./certs --relay-hosts 127.0.0.1,localhost
latch-control serve --listen 127.0.0.1:9000 --reg-token SECRET --ops-token OPS \
  --client-cert certs/control-plane.crt --client-key certs/control-plane.key --relay-ca certs/ca.crt
# open http://127.0.0.1:9000 for the panel
```

For production add `--db 'postgres://user:pass@host/latchcontrol?sslmode=require'`
(the schema migrates on start).

### The customer flow

```sh
# sign up (panel or API) → session cookie + CSRF token
curl -c jar -D - -X POST localhost:9000/api/signup -d '{"email":"a@b.com","password":"password123"}'
# create a token → placed on a relay over mTLS, returns a connection string + hostname
curl -b jar -H "X-CSRF-Token: <csrf>" -X POST localhost:9000/api/tokens -d '{"name":"laptop"}'
# → {"connectionString":"latch://…","hostname":"<slug>-laptop.eu.latch.app"}
# the customer runs the agent with it (no operator involved):
latchd relay add --url "<connectionString>"
latchd route add --target 127.0.0.1:3000 --tunnel laptop --hostname <slug>-laptop.eu.latch.app
```

Each account gets an account-prefixed hostname under the regional base domain, so
one wildcard certificate covers it and tenants cannot claim each other's names.
`POST /api/mfa/setup` + `/api/mfa/enable` turn on TOTP; `POST /api/apikeys` issues
programmatic keys (used via `Authorization: Bearer lk_…`).

### Operator / abuse

```sh
curl -H "Authorization: Bearer OPS" -X POST localhost:9000/ops/accounts/<id>/suspend -d '{"reason":"abuse"}'
curl -H "Authorization: Bearer OPS" -X POST localhost:9000/ops/blocklist -d '{"pattern":"phish.example","reason":"phishing"}'
```

Suspend revokes the account's tokens on their relays (the relay drops live
sessions within its revocation window) and blocks the account from the API.

### What the S2 tests cover

`go test ./latch-control/internal/store/` runs a conformance suite against the
in-memory store (and, with `LATCH_CONTROL_DB` set, PostgreSQL): account/user
creation, email-conflict, **tenant isolation** (B cannot see or mutate A's
token), suspend, blocklist, audit and API keys. `go test ./latch-control/internal/app/`
covers signup + self-service token, isolation (404 across tenants), suspend
revoking tokens, and CSRF enforcement. The full self-service → live-tunnel →
isolation → suspend chain was verified by hand with the real binaries.

## SaaS S3 — plans, quota, metering, billing

The managed SaaS is now monetizable. Billing is via a **Merchant-of-Record**
(Paddle/LemonSqueezy), which handles VAT/sales tax (the S0 decision; this
supersedes any "Stripe" wording in the roadmap).

### Plans and quotas

The catalog (free/pro/team) lives in `internal/plan`. Each plan caps tokens,
custom domains, concurrent tunnels, monthly bandwidth and request rate. Token and
domain creation refuse over-cap requests with HTTP 402 and an upgrade prompt. A
background loop warns at 80% of the bandwidth allowance and, for hard-capped
plans (free), suspends the account at 100%.

### Metering

The relay tracks usage per token and reports it in each heartbeat; the control
plane computes per-token deltas (reset-safe across relay restarts) and rolls them
up per account per day. `GET /api/usage` shows the account's current-month usage
against its plan.

### Billing webhooks

```sh
latch-control serve … --webhook-secret <hmac> --checkout-url https://pay.example/checkout
```

`POST /webhooks/mor` is HMAC-verified (`X-Mor-Signature`) and idempotent. Events
map as: `subscription.active`→set plan + resume, `payment.failed`→suspend,
`subscription.canceled`→downgrade to free. `POST /api/billing/checkout` returns a
hosted-checkout link carrying the account id as passthrough.

### Operator & compliance

```sh
curl -H "Authorization: Bearer OPS" localhost:9000/ops/accounts   # accounts + usage + plan
curl -H "Authorization: Bearer OPS" localhost:9000/ops/revenue    # MRR, accounts by plan
curl -b jar localhost:9000/api/account/export                     # GDPR/KVKK data dump
curl -b jar -H "X-CSRF-Token: …" -X DELETE localhost:9000/api/account   # right to erasure
```

### What the S3 tests cover

`go test ./latch-control/internal/app/` covers plan token caps, metering deltas
(cumulative heartbeats → correct rollup), the billing webhook (upgrade, payment
failure → suspend, recovery → resume, idempotency, bad signature). The full chain
(free cap → webhook upgrade → cap lifts → metered usage → payment-failure suspend
cuts the tunnel) was verified by hand with the real binaries.
