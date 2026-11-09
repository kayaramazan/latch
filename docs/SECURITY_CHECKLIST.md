# Security checklist (Phase 12)

Each security requirement from SPEC/docs/SECURITY.md mapped to where it is
enforced and the test that proves it. Audited 2026-11-09 for v1.0.0.

| # | Requirement | Enforced in | Proof |
|---|-------------|-------------|-------|
| 1 | Tokens stored only as argon2id hash | `latch-relay/internal/auth/auth.go` (`hashSecret`, 1 pass / 64 MiB / 4 lanes); `latch-control/internal/auth` (passwords) | `auth_test.go`; `TestPasswordHashRoundTrip` |
| 2 | Token revoke propagates to live sessions | `relay.watchRevocations` (≤30s poll) + `control` revoke | `relay_test.go: TestSupersedeAndRevoke`; S2/S3 suspend smoke cuts the tunnel |
| 3 | Token scope enforced (hostnames / TCP) | `auth.ScopeAllows`, `registry.checkSpec` | `relay_test.go: TestAuthScopeAndConflicts` |
| 4 | No hostname takeover between tenants | `registry` first-come + conflict codes; SaaS account-prefixed hostnames | `TestAuthScopeAndConflicts`; S2 isolation test + smoke |
| 5 | Relay has no public admin API | management is CLI-only; the control API is **mTLS + private + flag-gated off** | `control_test.go: TestMTLSAndTokenLifecycle` (no client cert → refused); self-hosted leaves `--control` off |
| 6 | TLS verification never skipped | agent verifies relay (system roots + explicit dev CA); control↔relay mTLS; no `InsecureSkipVerify` in the tree | grep: 0 `InsecureSkipVerify` (incl. tests) |
| 7 | Frame / header size limits | `tunnel.MaxFrameSize` 1 MiB, `MaxStreamHeaderSize` 64 KiB; `MaxBytesReader` on every JSON body | `frame_test.go`; `FuzzRecv`, `FuzzReadStreamHeader` (2M+ execs, no crash) |
| 8 | Slowloris / header timeouts | every `http.Server` sets `ReadHeaderTimeout` (api, ingress, control, metrics, latch-control) | audit: 6/6 servers |
| 9 | Connection limits (per-IP + total) | `ingress.LimitListener` (`DefaultMaxConns`, `DefaultMaxConnsPerIP`) | `ingress.go` |
| 10 | Client-IP from trusted proxies only | `policy.ClientIP` walks XFF only through trusted CIDRs | `policy_test.go: TestClientIP` (spoofed XFF ignored) |
| 11 | Management API auth off loopback | `api.requireAuthOrLoopback` refuses non-loopback without a password; session + CSRF; login rate-limit | `auth_test.go: TestNonLoopbackRequiresPassword, TestAuthRequiredAndLogin, TestOriginCSRFDefense` |
| 12 | Control-plane tenant isolation | every tenant row scoped by `account_id`; store returns NotFound across tenants | `store_test.go` conformance; `app_test.go: TestTenantIsolation` |
| 13 | Billing webhook authenticity | HMAC-SHA256 signature verification + idempotency | `app_test.go: TestWebhookBadSignature, TestBillingUpgradeFailResume` |
| 14 | Secrets handling | relay tokens argon2id-hashed on the relay; control-plane passwords argon2id; API keys sha256; mTLS keys 0600; no secrets in logs | code review; `config` 0600 writes |
| 15 | Protocol codec robustness | length-prefixed JSON with bounds; fuzzed | `FuzzRecv`, `FuzzReadStreamHeader`, `FuzzParseConnString` (no crashes) |

## Open findings

None blocking v1.0.0. Deferred, non-blocking hardening (see ROADMAP_NEXT):

- Distributed rate limiting across a relay fleet (per-relay today).
- A WAF / request-body inspection layer (out of scope; we proxy, not inspect).
- Formal third-party pentest before large-scale paid launch (controls are in
  place; certification is a process item).
- latch-control body `ReadTimeout`/`IdleTimeout` tuning under a real LB.

## Method

- `grep` for `InsecureSkipVerify` across the tree: 0 hits.
- `grep` every `http.Server{` and confirmed `ReadHeaderTimeout`: 6/6.
- `go test -fuzz` on the three protocol/parse targets: 2M+ execs each, no crash.
- Reviewed each table row against the cited code and test.
