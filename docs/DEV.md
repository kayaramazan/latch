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
