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
