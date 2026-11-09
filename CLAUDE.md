# Latch — working rules for Claude Code

Latch is an open-source local reverse proxy + self-hosted tunnel, managed from a
UI. Two Go binaries (`latchd` agent with embedded Caddy, `latch-relay` relay),
one React UI served by the agent, a thin Electron shell.

## Read first, every session
1. `SPEC.md` — the product requirements (source of truth).
2. `ROADMAP.md` — phased plan. Work is done **one phase at a time**, in order.
3. `NOTES.md` — decisions already made; do not re-decide them.
4. `CHANGELOG.md` — what already exists.

## How work happens
- Implement only the phase the user names. Do not start the next phase; put
  discovered follow-ups in `NOTES.md` under "Backlog".
- Leave every phase in a working, tested state. Go: `make test`; UI: `make test-ui`.
- When something is ambiguous: do not invent assumptions. Pick the smallest safe
  option, record it in `NOTES.md` (date, decision, why, alternatives), and tell
  the user. Ask before decisions that affect the user-facing product.
- At the end of a phase: verify each DoD item in `ROADMAP.md`, tick the boxes,
  add a `CHANGELOG.md` entry under `[Unreleased]`, commit as `phase N: <summary>`.

## Conventions
- Code, comments, docs in English. UI strings go through i18n (en default, tr).
- Go 1.24+ (go.mod floor; Caddy needs it), `slog` for logging, `modernc.org/sqlite`, `hashicorp/yamux`,
  Caddy v2 as a library (agent only), certmagic (relay), Prometheus client.
- UI: React + TypeScript + Vite, TanStack Query, Tailwind, Recharts; pnpm 9, Node 22.
- TLS certificate verification is never skipped, not even in dev — use the
  relay's dev CA file.
- Module path `github.com/kayaramazan/latch`, single go.mod, Apache-2.0.
- Tunnel control messages: length-prefixed JSON (see PROTOCOL.md). Do not switch encodings.
- Monorepo layout: `/latchd`, `/latch-relay`, `/latch-ui`, `/electron`, `/proto`,
  `/policy` (shared policy engine), `/docs`, `/deploy`.
