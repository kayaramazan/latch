# Latch managed SaaS — architecture (S0 design gate)

This document describes the **managed-relay SaaS**: the operator (us) hosts a
fleet of relays and sells tunneling as a service. It sits on top of the
open-source, self-hosted product (phases 0–12) and does not change it; the new
behaviour is behind flags. Self-hosted installs keep working exactly as before.

**Status:** design gate (S0). Decisions in "Resolved decisions" are approved;
S1 implements the relay control API and fleet registration.

## Components

```
                 ┌───────────────────────────── latch-control (new) ──────────────────────────┐
  customer ──►   │  web app + REST API  ·  accounts/auth (email+pw+TOTP)  ·  self-service      │
  (browser/CLI)  │  tokens & domains  ·  placement  ·  plans/quotas/metering  ·  billing (MoR) │
                 │  admin/ops console  ·  abuse pipeline  ·  audit log   —   PostgreSQL          │
                 └───────┬──────────────────────────────────────────────────┬──────────────────┘
                         │ control API (mTLS, private network, flag-gated)   │ usage/metering push
                         ▼                                                   ▲
          ┌──────────── latch-relay fleet (existing binary + control API) ───────────┐
          │  region eu:  relay-eu-1, relay-eu-2 …   wildcard *.eu.latch.app           │
          │  TLS termination · SNI/Host routing · per-token policy · /metrics          │
          └───────▲───────────────────────────────────────────────────────────────────┘
                  │ tunnel (TLS + yamux, unchanged)
            ┌─────┴─────┐
            │  latchd    │  the customer runs the agent where their service lives;
            │  (agent)   │  it connects to the relay the control plane assigned.
            └───────────┘
```

- **latch-control** — the multi-tenant brain. New Go service, its own
  PostgreSQL. Never on an agent or relay; the operator hosts it (HA).
- **latch-relay fleet** — the existing relay binary plus a new, private,
  flag-gated **control API** (S1). Relays are cattle: provisioned per region,
  each serving a regional wildcard (`*.eu.latch.app`).
- **latchd (agent)** — unchanged. It connects to the relay the control plane
  assigned, using a token the control plane issued.

## Key architectural decision: the relay control API

The self-hosted product's rule "the relay has no network admin API" is
**relaxed, in a scoped way**, only for the SaaS:

- The relay gains a **control API** that only the control plane calls.
- It is **mTLS-authenticated**, bound to a **private interface** (never the
  public internet), and **off by default** (a config flag turns it on). A
  self-hosted relay never enables it and keeps CLI-only management.
- It lets the control plane: create/revoke tokens and custom domains remotely,
  read health/capacity, and receive usage/metering. Everything a human does
  today with `latch-relay token create` etc., done remotely and audited.

## Data flow

1. **Onboard.** Customer signs up → `latch-control` creates an account, assigns
   a region/relay (placement), and issues a token via the relay control API. The
   agent gets a `latch://relay-eu-1.latch.app:7443/?token=…` string.
2. **Serve.** The agent connects to its assigned relay and announces routes
   exactly as today. Subdomains under `*.eu.latch.app` are covered by the
   regional wildcard — no per-subdomain ACME.
3. **Meter.** Relays push usage (bytes, connections, active routes, denials)
   to `latch-control`, which aggregates per account, enforces quotas and feeds
   billing.
4. **Bill.** `latch-control` syncs plan/usage to the **Merchant-of-Record**
   (Paddle/LemonSqueezy); MoR webhooks drive plan changes and suspend/resume.

## Placement & regions

- v1: a single region (EU) with one wildcard base domain `*.eu.latch.app`.
- An account is placed on a relay in its region at onboarding; the relay id is
  stored with the account's tokens. Re-placement (drain/move) is an operator
  action in S1+.
- Custom domains (CNAME to the assigned relay) use on-demand ACME with the
  phase-4 permission function, cached per relay.

## Trust boundaries

- **customer ↔ control plane**: session cookie + CSRF (reuse the phase-8
  pattern) or API key; every request authorized by `account_id`.
- **control plane ↔ relay**: mTLS on a private network; the relay control API is
  never public and is flag-gated.
- **agent ↔ relay**: unchanged tunnel (token auth over TLS).
- **control plane ↔ MoR / cert DNS provider**: secrets in a secrets manager/KMS,
  never in the database in plaintext.

## What the SaaS reuses vs. adds

- **Reuses:** the relay (TLS, routing, per-token policy, metrics), the agent, the
  tunnel protocol, the `/policy` engine, phase-7 events/metering, phase-8 auth
  patterns, phase-4 ACME + custom-domain permission function.
- **Adds:** `latch-control` (accounts, self-service, placement, quotas, billing,
  admin), the relay control API + fleet registration, per-account aggregate
  quota enforcement, the abuse pipeline, and compliance tooling.

## Deployment topology (v1)

- `latch-control`: 2+ instances behind a load balancer, PostgreSQL (primary +
  replica), secrets manager, region EU.
- relay fleet: N relays in EU, each with the control API on a private address,
  a regional wildcard cert, and Prometheus scraped by the operator.
- CI/CD and infra-as-code for relay provisioning land in S1/phase-11.
