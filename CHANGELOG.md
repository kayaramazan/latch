# Changelog

All notable changes to this project will be documented in this file.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

- Phase 2: tunnel protocol and relay core. `proto/tunnel` (control-message
  envelope + length-prefixed JSON codec, data-stream header/status, connection
  string, version negotiation, yamux config; fuzzed). latch-relay: SQLite store
  (tokens, routes, settings), argon2id token auth with scopes, hostname/TCP-port
  registry with full-set announce and offline persistence, agent session
  (handshake, ping/pong, stream opening, supersede/limits), ingress (reverse
  proxy over tunnel streams, TCP tunnels, 404/502/503 pages, connection limits),
  dev CA, and the `latch-relay` CLI (run, token create/list/revoke, dev-ca).
  Integration tests cover HTTP/WebSocket/TCP end to end, scope/conflict
  rejection, supersede/revoke and restart persistence. See docs/DEV.md.
- Phase 1: shared data model (`proto/model`: types, defaults, whole-snapshot
  validation with field-level errors, diff with affected-route computation) and
  latchd config store (`latchd/internal/store`: SQLite via modernc, migrations,
  immutable config versions, rollback, diff, export/import replace|merge,
  change subscriptions, pruning) plus data-dir resolution (`latchd/internal/config`).
- Project renamed from Gate to Latch.
- Phase 0: design documents (docs/ARCHITECTURE.md, docs/DATA_MODEL.md,
  PROTOCOL.md draft, docs/API.md, docs/SCREENS.md, docs/SECURITY.md), repository
  skeleton (go.mod, placeholder binaries, Makefile, CI, lint config, license),
  README. No feature code yet.
- Project planning: SPEC.md, ROADMAP.md, CLAUDE.md, NOTES.md.
