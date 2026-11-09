# Security

This maps SPEC's security requirements to concrete design decisions. Phase 12
re-audits every row.

## Assets and trust boundaries

- **Agent tokens** — grant the right to publish hostnames on a relay.
- **Management session** — full control of the agent (routes, policies, relays).
- **Relay host** — anyone who controls it controls traffic for all its hostnames
  (single-owner model; it is the user's own VPS).
- **Local targets** — services behind the agent that were never meant to be public.

Trust boundaries: Internet ↔ relay public listeners; Internet ↔ agent tunnel
listener (`:7443`); UI/CLI ↔ agent management API; relay ↔ agent (tunnel).

## Threat model → controls

| Threat | Control | Where |
|--------|---------|-------|
| Stolen relay database leaks tokens | Tokens are random 32-byte values; only `argon2id(token)` is stored. Plain token is shown once at creation. | relay `auth` |
| Token used beyond its purpose | Every token carries a scope: explicit hostnames and/or wildcard patterns, optional TCP port allowance. `RouteAnnounce` outside scope is rejected. | relay `registry` |
| Token leaked | `latch-relay token revoke` takes effect immediately: the session is closed and routes withdrawn. Tokens have optional expiry. | relay `auth` |
| Hostname takeover by another agent | A hostname binds to the token that first announced it; another token cannot claim it until the owner withdraws it or the relay admin releases it. Two sessions of the *same* token: newest wins, oldest is told `ERR_SUPERSEDED`. | relay `registry` |
| Relay management exposed to the Internet | There is no network admin API on the relay. Admin is CLI-only on the host; configuration from the agent happens only inside the authenticated tunnel. | relay |
| Agent management UI exposed | Default bind `127.0.0.1`. Non-loopback bind refuses to start without a password. Sessions are HttpOnly, SameSite=Strict cookies; CSRF double-submit header on mutations; login rate-limited (5/min/IP). | agent `api` |
| Spoofed client IP | Relay public side: peer IP only, XFF stripped and rewritten. Agent local mode: XFF accepted only from the configured trusted proxy list. Tunnel streams carry the client IP in `StreamHeader`, which only the relay writes. | `policy`, ingress |
| Slowloris / resource exhaustion on relay | `ReadHeaderTimeout` 10s, idle timeout 120s, max header 64 KiB, max concurrent connections per IP and total, max streams per agent session, bounded frame size (1 MiB control, raw data streams uncapped but counted). | ingress, `session` |
| ACME abuse via custom domains | On-demand issuance only for hostnames that are (a) announced by a scoped token and (b) resolve (CNAME/A) to the relay. Rate limited per hostname. | relay `acme` |
| Downgraded / unverified TLS | Agents verify the relay certificate against system roots; a CA file may be *added* for dev, never `InsecureSkipVerify`. Relay↔ACME and agent↔local API in Electron are the same. | agent `tunnel` |
| Malicious policy or route payload from a compromised agent | Relay validates every `PolicySync` and `RouteAnnounce` with the shared `model.Validate`; sizes are bounded; a bad message is an error, never a crash. | `proto`, relay |
| Config rollback to a bad state | Every version is validated before being written; rollback re-validates. Caddy load failure keeps the previous running config. | agent `store`, `local` |
| Secrets at rest on the agent | Relay tokens and DNS provider credentials are stored in `latchd.db` with file mode `0600` in a `0700` directory. OS keychain integration is out of scope for v1 (see NOTES). | agent `config` |
| Supply chain | Release binaries built in CI with pinned toolchain, checksums and cosign signatures (phase 11). Dependencies are minimal and reviewed. | CI |

## Operational guidance (to be expanded in phase 11 docs)

- Keep `:7443` open only to the agents' networks if you can (firewall).
- Rotate tokens when a device is lost (`token revoke` + `token create`).
- The relay serves its own hostname's certificate on `:7443`; do not put it
  behind another TLS terminator.
