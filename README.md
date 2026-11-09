# Latch

**Local reverse proxy + self-hosted tunnel, managed from a UI.**

Latch combines two things in one open-source product:

- **Local mode** — a classic reverse proxy on a server with a public IP
  (domain → upstream) with automatic HTTPS, configured from a web UI instead
  of config files.
- **Tunnel mode** — expose a port on a machine behind NAT ("share my port
  3000") through a relay you run on your own VPS. No third-party service.

Priorities, in order: reliability, simplicity, observability. The target
experience: install, pick a port or a domain, be live with valid HTTPS in two
minutes.

## Components

| Component     | What it is                                                                 |
|---------------|----------------------------------------------------------------------------|
| `latchd`      | Agent/daemon (Go). Embeds Caddy for local mode, runs tunnel clients, serves the management API and the UI. |
| `latch-relay` | Relay server (Go). Runs on your VPS, terminates TLS, routes by hostname to connected agents, enforces IP/rate-limit policies. |
| `latch-ui`    | Web UI (React + TypeScript), served by `latchd`.                          |
| `electron`    | Desktop shell (tray, start/stop the daemon, auto-update) — no business logic. |

## Status

**v1.0.0.** The self-hosted product (tunnel + local mode, policies,
observability, authenticated API, web UI) is complete and hardened; a
managed-relay SaaS control plane (`latch-control`) is also included. See
[CHANGELOG.md](CHANGELOG.md), [ROADMAP.md](ROADMAP.md) (Turkish) and
[docs/ACCEPTANCE.md](docs/ACCEPTANCE.md).

## Quick start

### Scenario A — share a local port over a tunnel

On a server with a public IP, install the relay and follow the wizard (domain,
email, DNS). It prints a `latch://…` connection string:

```sh
curl -sSL https://github.com/kayaramazan/latch/releases/latest/download/install.sh | sh
# → paste the connection string below
```

On the machine with the service you want to expose:

```sh
# install the agent (or: docker run ghcr.io/kayaramazan/latchd:latest)
latchd relay add --url 'latch://relay.example.com:7443/?token=…'
latchd route add --target 127.0.0.1:3000 --tunnel <relay> --hostname app.relay.example.com
# → https://app.relay.example.com is live with valid HTTPS
# (the web UI auto-detects your listening ports and suggests them)
```

Open `http://127.0.0.1:4040` for the web UI (set a password with
`latchd auth set-password` to reach it from elsewhere).

### Scenario B — local reverse proxy on a public-IP server

Run the agent on the server itself and let it get certificates automatically:

```sh
latchd route add --local --hostname site.example.com --target 127.0.0.1:8080
# → https://site.example.com is served by the embedded Caddy
```

Both scenarios are the same binary, managed from the same UI. Run everything in
containers with `deploy/docker-compose.tunnel.yml`.

### Run it as a service

```sh
sudo latchd install     # systemd (Linux) or launchd (macOS)
latchd upgrade          # fetch and install the latest release
```

## Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — repository layout, components, data flows, ports
- [docs/DATA_MODEL.md](docs/DATA_MODEL.md) — shared data model and config versioning
- [PROTOCOL.md](PROTOCOL.md) — tunnel protocol
- [docs/API.md](docs/API.md) — management REST/WebSocket API
- [docs/SCREENS.md](docs/SCREENS.md) — UI screens and flows
- [docs/SECURITY.md](docs/SECURITY.md) — threat model and security design
- [NOTES.md](NOTES.md) — decision log

## Development

Requirements: Go 1.24+, Node 22 + pnpm 9 (UI, from phase 9).

```sh
make build        # bin/latchd, bin/latch-relay, bin/latch-control
make test         # unit + race
make test-ui      # UI component tests + i18n completeness
sh deploy/acceptance/run.sh   # the three SPEC acceptance scenarios
make lint         # needs golangci-lint
```

More: [docs/DEV.md](docs/DEV.md) (walkthroughs), [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md),
[docs/SECURITY_CHECKLIST.md](docs/SECURITY_CHECKLIST.md), [docs/COMPAT.md](docs/COMPAT.md),
[docs/openapi.yaml](docs/openapi.yaml).

## License

Apache-2.0. See [LICENSE](LICENSE).
