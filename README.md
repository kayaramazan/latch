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
| `electron`    | Thin desktop shell around the UI (tray, start/stop daemon, auto-update). |

## Status

Pre-alpha. The project is being built phase by phase; see
[ROADMAP.md](ROADMAP.md) (Turkish) and [CHANGELOG.md](CHANGELOG.md).

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
make build   # bin/latchd, bin/latch-relay
make test
make lint    # needs golangci-lint
```

## License

Apache-2.0. See [LICENSE](LICENSE).
