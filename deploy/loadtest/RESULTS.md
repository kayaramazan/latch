# Load test results

`deploy/loadtest` fires concurrent HTTPS requests through a tunnel and reports
throughput and latency. `deploy/loadtest/run.sh [concurrency] [duration]` sets up
a dev relay + agent + a trivial local server and measures the full tunnel path
(client → relay TLS termination → tunnel → agent → local service).

## Recorded run (2026-11-09, local dev, M-series laptop)

```
# sh deploy/loadtest/run.sh 50 10s
requests: 22069  ok: 22068  errors: 1  rps: 2207
latency  p50: 7.1ms  p95: 67.5ms  p99: 177.8ms
```

The single relay sustains ~2.2k req/s over the tunnel with single-digit-ms median
latency on a laptop, client and server on the same host (so this measures the
relay+tunnel overhead, not the network). It is a smoke-grade number to catch
regressions, not a capacity-planning benchmark; a real deployment measures on the
target VPS with realistic network latency and payload sizes.

## Notes

- Connection and per-IP caps (`ingress.LimitListener`) and per-token stream
  limits bound resource use under load.
- Rate-limit policies (phase 5) cut abusive clients before they reach the agent.
- Distributed rate limiting across a relay fleet is out of v1 scope
  (see ROADMAP_NEXT).
