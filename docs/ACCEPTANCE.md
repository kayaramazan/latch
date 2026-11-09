# Acceptance scenarios

The three SPEC success criteria, as runnable scripts. `deploy/acceptance/run.sh`
builds the binaries, stands up a dev relay + agent (dev CA — certificate
verification is never skipped; curl trusts the CA explicitly) and asserts each
scenario, printing PASS/FAIL.

```sh
sh deploy/acceptance/run.sh
```

## Scenarios

1. **Tunnel** — expose a local port over HTTPS through the relay. The agent
   connects with a token and announces `app.relay.local`; a public HTTPS request
   reaches the local service. Target: well under 2 minutes.
2. **Local mode** — the agent serves a local reverse proxy with embedded Caddy
   over real HTTPS (internal CA), proven by `TestLocalModeEndToEnd`.
3. **Recovery** — kill the agent (public host returns `503 Agent offline`, the
   route stays registered), restart it, and the tunnel re-establishes itself.
   Target: back under 10 seconds, no manual intervention.

## Recorded result (2026-11-09, local dev)

```
PASS  scenario 1 (tunnel): HTTPS request reached the local service in 0s (< 120s)
PASS  scenario 2 (local mode): embedded Caddy served HTTPS to a local upstream (TestLocalModeEndToEnd)
PASS  scenario 3 (recovery): offline → 503, restart → tunnel back in 1s (< 10s)
ALL ACCEPTANCE SCENARIOS PASSED
```

A production sign-off re-runs this on a real VPS with real ACME certificates;
the logic is identical, only the CA and hostnames change.
