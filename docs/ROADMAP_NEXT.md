# Roadmap — after v1.0

Explicitly out of scope for v1.0, recorded so they are not forgotten. None block
the v1.0 success criteria; they are scale, breadth and ecosystem items.

## Transport & performance
- **QUIC / HTTP/3 end to end through the tunnel.** Today HTTP/2 terminates at the
  relay and the tunnel carries HTTP/1.1 to the agent; local mode already offers
  HTTP/3 on the public side. End-to-end h3/gRPC needs h2c/h3 to the agent.
- **Distributed rate limiting** across a relay fleet (per-relay today).
- Relay **clustering / HA** with shared routing state (one relay per account at
  v1; placement is static).

## Security & abuse
- A **WAF / request inspection** layer (we proxy, we do not inspect bodies).
- Automated **domain reputation** feeds and a richer abuse/DMCA workflow.
- A formal third-party **penetration test** before large-scale paid launch.
- OS **keychain** integration for agent-side secrets (0600 files today).

## Product breadth
- **OAuth / SSO / SAML** for the SaaS (email+password+TOTP at v1).
- Multi-region **data residency** (single EU region at v1).
- A full **admin UI** for the SaaS operator console (API + minimal panel at v1).
- **GeoIP database upload** from the UI to the relay over the tunnel.
- Team **seats & roles** UI, usage alerts, and a billing history view.
- Mobile-friendly **latch-ui** layout and code-splitting (Recharts bundle).
- **Electron** desktop shell (phase 10 — deferred; not required for the server
  or SaaS use cases).

## Operations
- `tls: off` plain-HTTP local routes; a zapcore→slog bridge for Caddy's logs.
- Packaging breadth (Docker images, OS packages, Homebrew) — phase 11.
