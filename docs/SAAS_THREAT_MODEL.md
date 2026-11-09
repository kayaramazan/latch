# Latch managed SaaS — threat model (S0 design gate)

Scope: the managed-relay SaaS, where we host relays that terminate TLS for and
proxy *other people's* traffic under *our* domains. That is the central risk and
shapes everything below.

## Assets

- Customer traffic in transit (we terminate TLS → we can see plaintext).
- Customer secrets: tunnel tokens, custom-domain control, account credentials.
- Operator secrets: relay mTLS keys, MoR/API keys, DNS-provider credentials.
- Our reputation and IP space (abuse under `*.latch.app` burns both).
- Availability of the relay fleet and control plane.

## Trust boundaries & controls

| Boundary | Threat | Control |
|---|---|---|
| Internet → relay public :443 | DDoS, scanning, abuse | per-IP + total conn caps (exist), rate limits, upstream DDoS (Cloudflare/anycast) |
| Internet → control plane | credential stuffing, CSRF | argon2id, login rate-limit, TOTP, SameSite+CSRF (phase-8 pattern), WAF |
| control plane → relay control API | forged control calls, lateral movement | **mTLS**, **private network only**, **flag-gated off by default**, per-call audit |
| agent → relay | token theft/replay | tokens are argon2id-hashed on the relay, scoped, revocable; TLS 1.3 |
| tenant → tenant | cross-tenant access | `account_id` scoping everywhere + relay per-token isolation; optional PG RLS |
| control plane → MoR / DNS | key leak | secrets manager/KMS, least privilege, rotation, never in DB plaintext |

## Top risks (managed-hosting specific)

1. **Phishing / malware / illegal content under our subdomains.**
   This is the #1 operational risk. Mitigations:
   - Domain/URL reputation check on subdomain creation and periodically
     (Google Safe Browsing / similar).
   - Fast **suspend** flow (control plane revokes the token + withdraws routes
     on the relay within seconds).
   - Abuse report intake (`abuse@`), DMCA process, documented in ToS/AUP.
   - We **proxy, not host**: no customer content stored; reduces liability but
     not the takedown duty.
   - New/free accounts get tighter limits and reputation gating.

2. **TLS-termination liability / data exposure.**
   We can see plaintext. Controls: never log request/response bodies; metering
   is counters only (`usage_rollup` has no contents); encrypt secrets at rest;
   a clear DPA stating we are a processor; minimize data retention.

3. **ACME rate-limit exhaustion / cert abuse.**
   Regional wildcard (`*.eu.latch.app`) means subdomains cost no ACME issuance.
   Custom domains use on-demand ACME with the phase-4 permission function (only
   registered + correctly-pointed domains), cached, behind a rate-limit-aware
   queue per relay. An attacker cannot drive issuance for names they don't
   control.

4. **Noisy-neighbour / resource abuse.**
   Relay enforces per-token session/route/stream and rate limits (exist);
   control plane enforces per-account concurrency and monthly bandwidth from
   `usage_rollup`; global per-relay caps; fair-use in the AUP.

5. **Billing fraud / abuse of free tier.**
   MoR handles card fraud/chargebacks; free tier gets low limits, reputation
   gating, and no custom domains until verified.

6. **Compromise of a single relay.**
   Blast radius = that relay's tenants. Controls: relays hold only hashed
   tokens and their regional cert; mTLS client cert scoped to the control API;
   rapid rotation; no cross-relay secrets.

## Compliance (v1: GDPR + KVKK, single region)

- Legal docs: Terms of Service, Acceptable Use Policy, Privacy Policy, DPA.
- Data subject rights: export and delete (see data model).
- Audit log of account/admin/system actions.
- Single region (EU) to start; multi-region residency is deferred (v2).
- Sub-processor list (MoR, DNS provider, cloud, email) published.

## Explicitly deferred (documented, not v1)

- Multi-region data residency and regional isolation.
- OAuth/SSO/SAML.
- Own intermediate CA for subdomains (wildcard suffices at v1 scale).
- SOC 2 / ISO 27001 formal certification (follow the controls now, certify later).
