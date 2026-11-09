# Version & protocol compatibility

## Tunnel protocol

The agent and relay negotiate a protocol version in the `Hello`/`HelloAck`
handshake. Each build advertises a supported range `[MinProtocolVersion,
ProtocolVersion]` (`proto/tunnel/messages.go`) and `Negotiate` picks the highest
version both support.

| | Value (v1.0.0) |
|---|---|
| `ProtocolVersion` | 1 |
| `MinProtocolVersion` | 1 |
| ALPN | `latch/1` |

A newer relay keeps serving older agents (and vice versa) as long as their
ranges overlap. When they do **not** overlap:

- The relay replies with `ERR_VERSION` carrying its `relayMin`/`relayMax`.
- The agent prints a clear, actionable message naming which side to update, e.g.
  `protocol mismatch: relay supports 2-2, this agent supports 1-1 (update the
  agent side)` (`latchd/internal/tunnel/conn.go`).
- In the UI, the relay card shows the error under its sync/connection state; the
  agent keeps retrying with backoff so it recovers automatically once the
  mismatch is resolved.

Bumping the protocol: raise `ProtocolVersion`, keep `MinProtocolVersion` at the
oldest still-supported version, and gate new message fields on the negotiated
version. The control-message envelope is versioned JSON, so additive fields do
not break older peers.

## Control API (SaaS)

The relay control API is versioned by path (`/control/v1`). A new major version
adds `/control/v2` while the old one keeps working during migration.

## Binaries & releases

Release artifacts are built by goreleaser (`.goreleaser.yaml`) for linux and
darwin on amd64/arm64 (plus windows for the agent), with a cosign-signed
`checksums.txt`. Container images are published to `ghcr.io/<owner>/{latchd,
latch-relay,latch-control}` as multi-arch (amd64/arm64). `deploy/install.sh
--version vX.Y.Z` installs a specific release.
