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
