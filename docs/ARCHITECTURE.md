# Architecture

Latch is one product with two deployment shapes that share code, a data model
and a policy engine.

```
                 LOCAL MODE                            TUNNEL MODE
   (server with a public IP)             (laptop/NAS behind NAT  +  your VPS)

   Internet ──:443──▶ latchd ──▶ upstream     Internet ──:443──▶ latch-relay ══tunnel══▶ latchd ──▶ localhost:3000
                      │ embedded Caddy                         │ TLS, SNI routing,          │ yamux client,
                      │ + latch_policy                         │ policy engine              │ reconnect
                      ▼                                        ▼                            ▼
                  web UI / API                             /metrics                     web UI / API
                                                      (access events ◀══ streamed back ══▶ one UI shows all)
```

## Repository layout (single Go module)

```
latch/
├── go.mod                  module github.com/kayaramazan/latch
├── latchd/                 agent binary (package main) + latchd/internal/...
│   └── internal/
│       ├── store/          SQLite config store, versioning, rollback, backup      (phase 1)
│       ├── config/         data dir, settings, file permissions                   (phase 1)
│       ├── tunnel/         relay client(s), reconnect, route announce             (phase 3)
│       ├── api/            REST + WS management API, auth, embedded UI            (phase 3, 8)
│       ├── local/          model → Caddy JSON, embedded Caddy, latch_policy module (phase 6)
│       ├── health/         upstream health checker                                (phase 6)
│       └── events/         unified event store, retention, stats, explain         (phase 7)
├── latch-relay/            relay binary (package main) + latch-relay/internal/...
│   └── internal/
│       ├── auth/           agent tokens (hashed), scopes, revocation               (phase 2)
│       ├── registry/       hostname → agent session, conflict rules              (phase 2)
│       ├── session/        yamux session handling, control stream                 (phase 2)
│       ├── ingress/        :443/:80 listeners, SNI/Host routing, TCP ports        (phase 2)
│       ├── store/          relay SQLite (tokens, routes, policy snapshots)        (phase 2)
│       ├── acme/           certmagic: wildcard DNS-01, TLS-ALPN-01, on-demand     (phase 4)
│       └── install/        install wizard, systemd unit, doctor                   (phase 4)
├── proto/
│   ├── model/              shared data types + validation                          (phase 1)
│   └── tunnel/             protocol messages, framing, version negotiation        (phase 2)
├── policy/                 shared policy engine (IP/CIDR, GeoIP, rate limit, headers, explain) (phase 5)
├── latch-ui/               React + TS + Vite web app, embedded into latchd        (phase 9)
├── electron/               thin desktop shell                                      (phase 10)
├── deploy/                 install.sh, Dockerfiles, compose, systemd, grafana      (phase 4, 11)
├── docs/                   design docs (this folder)
├── PROTOCOL.md             tunnel protocol specification
├── SPEC.md, ROADMAP.md     product spec and phased plan (Turkish)
└── NOTES.md, CHANGELOG.md  decision log, change log
```

Rules:
- `proto/` and `policy/` are the only packages imported by **both** binaries.
  They must not import anything from `latchd/` or `latch-relay/`.
- Each binary's `internal/` is private to that binary.
- Caddy is a dependency of `latchd` only. The relay never links Caddy; it uses
  `certmagic` directly.

## Components

### latchd (agent)

A single process with these long-running parts, all supervised by a root
context and shut down gracefully:

| Part | Responsibility |
|------|----------------|
| Store | SQLite config store. Every mutation produces a new immutable config version. Emits change events to the other parts. |
| API server | REST + WebSocket on `127.0.0.1:4040` by default. Serves the embedded UI. Auth is mandatory when bound to a non-loopback address. |
| Tunnel manager | One client per configured relay. Announces tunnel routes, forwards streams to local targets, reconnects with exponential backoff, pushes policy snapshots, receives access events. |
| Local proxy | Embedded Caddy. Config is derived from the model and applied with zero-downtime `caddy.Load`; a failed load keeps the previous config. The `latch_policy` handler runs the shared policy engine for local routes. |
| Health checker | Probes upstreams of all enabled routes; status is exposed via API and shown in the UI. |
| Event store | Ring buffer + SQLite table of access/system events from both modes, with retention. Feeds the live stream, logs, dashboards and `/metrics`. |

### latch-relay

| Part | Responsibility |
|------|----------------|
| Agent listener | `:7443` TLS. Authenticates agents by token, runs one yamux session per agent connection. |
| Registry | Maps hostnames (and TCP ports) to the live agent session that announced them. Enforces token scope and one-hostname-one-token. Remembers routes while the agent is offline so policies still apply and a proper 503 is served. |
| Ingress | `:443` TLS (certmagic) and `:80` redirect. Looks up the session by SNI/Host, runs the policy engine, then forwards the raw connection into a new tunnel stream. Serves 503 (agent offline) and 404 (unknown host) pages. Dedicated listeners for TCP tunnels. |
| Policy engine | Shared `policy` package. Snapshots are pushed by agents and persisted per token. |
| ACME | certmagic with wildcard DNS-01 (Cloudflare, Route53, Hetzner, DigitalOcean, manual TXT) or per-hostname TLS-ALPN-01; on-demand issuance for verified custom domains. |
| Admin | CLI only (`token create/list/revoke`, `status`, `doctor`). No network admin API. `/metrics` on `127.0.0.1:7070`. |

### latch-ui and electron

The UI is a static bundle embedded in `latchd` via `go:embed` and talks only
to the management API. Electron loads `http://127.0.0.1:<port>` in a window,
manages the daemon process lifecycle and the tray icon, and self-updates. It
contains no business logic.

## Data flows

### Tunnel request

1. Client connects to `https://app.relay.example.com`. Relay terminates TLS
   (wildcard cert), reads SNI/Host.
2. Registry resolves hostname → route → agent session (or `offline`).
3. Policy engine evaluates the route's snapshot: client IP (direct peer; XFF
   is never trusted on the relay's public side), IP/CIDR rules, GeoIP, rate
   limit. Denied requests are answered at the relay and never enter the
   tunnel. An `AccessEvent` is emitted either way.
4. Relay opens a yamux stream to the agent, writes a `StreamHeader` (route id,
   client ip/port, hostname), then copies bytes in both directions. For HTTP
   it also injects `X-Forwarded-For/-Proto/-Host` into the first request.
5. Agent reads the header, dials the local target, writes a one-byte status,
   then copies bytes. On dial failure the relay serves 502.
6. The relay streams the `AccessEvent` to the agent; the agent stores it so
   the user sees relay-side decisions in the same UI as local ones.

### Local request

1. Client connects to `https://site.example.com` on the server running
   `latchd`. Embedded Caddy terminates TLS (ACME HTTP-01/TLS-ALPN-01).
2. The `latch_policy` handler resolves the client IP (XFF honoured only from
   configured trusted proxies), evaluates the same policy objects, emits an
   `AccessEvent`.
3. `reverse_proxy` forwards to the upstream; health status is tracked.

### Configuration change

1. UI/CLI sends a mutation to the API.
2. Store validates the whole resulting config (`model.Validate`), computes the
   diff and the list of affected routes. A `dry-run` endpoint returns this
   without applying.
3. Store writes a new config version atomically.
4. Subscribers react: local proxy rebuilds Caddy JSON and loads it (keeping the
   old one on failure, surfacing field-level errors); tunnel manager sends
   `RouteAnnounce`/`RouteWithdraw` and `PolicySync` to affected relays and
   tracks ack versions; a missing ack marks the relay `out_of_sync`.
5. Rollback is just "write version N's snapshot as version M+1".

## Processes, ports and files

| Process | Listener | Default | Notes |
|---------|----------|---------|-------|
| latchd | management API + UI | `127.0.0.1:4040` | Configurable; non-loopback requires auth. `/metrics` lives here. |
| latchd | local mode HTTP/HTTPS | `:80`, `:443` | Only when at least one `local` route exists. HTTP/3 (UDP 443) on by default. |
| latch-relay | public HTTPS/HTTP | `:443`, `:80` | `:80` only redirects and serves ACME HTTP-01 if enabled. |
| latch-relay | agent tunnel | `:7443` TLS | Same certificate as `:443` (the relay's own hostname). |
| latch-relay | TCP tunnel pool | `20000-20999` | Range configurable; one port per TCP route. |
| latch-relay | metrics | `127.0.0.1:7070` | Prometheus `/metrics`. |

| Process | Data directory | Contents |
|---------|----------------|----------|
| latchd | Linux `/var/lib/latchd`, macOS `~/Library/Application Support/latchd`, Windows `%ProgramData%\latchd`, or `--data-dir` | `latchd.db` (config + events), `caddy/` (certs, ACME state), `logs/` |
| latch-relay | `/var/lib/latch-relay`, config in `/etc/latch-relay/config.yaml` | `relay.db`, `certs/`, `geoip/` |

All data directories are created with mode `0700`; databases and keys `0600`.

## Build and runtime constraints

- Go 1.24+ (go.mod), single static binary per component, `CGO_ENABLED=0`
  (SQLite via `modernc.org/sqlite`, pure Go).
- Logging via `log/slog`, JSON handler in production, text in dev.
- Both binaries expose `version` and `--help`; `latchd` and `latch-relay`
  refuse to run with mismatched protocol versions and say which side to update.
