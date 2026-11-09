# Latch managed SaaS — pricing & plans (S0 draft)

A starting point for S3 (billing). Numbers are placeholders to size the data
model and quota enforcement, not a committed price list. Billing is via a
**Merchant-of-Record** (Paddle/LemonSqueezy): they are the seller of record and
handle global VAT/sales tax.

| | Free | Pro | Team |
|---|---|---|---|
| Price | $0 | ~$9 / user / mo | ~$29 / user / mo |
| Tunnel tokens | 1 | 10 | 50 |
| Concurrent tunnels | 1 | 10 | 50 |
| Subdomains (`*.region.latch.app`) | 1 | unlimited | unlimited |
| Custom domains | — | 3 | 25 |
| Bandwidth / mo | 5 GB | 100 GB | 1 TB |
| Overage | hard stop | ~$0.10/GB | ~$0.08/GB |
| Rate limit (req/min) | low | higher | higher |
| Members (seats) | 1 | per-seat | per-seat |
| MFA | yes | yes | yes |
| Support | community | email | priority |

Enforcement:

- **Soft** (warn in UI + email) at 80% of bandwidth / token / tunnel limits.
- **Hard**: Free hits a hard stop at 100% bandwidth; paid plans meter overage.
  Over the token/domain/concurrency caps → creation refused with a clear
  upgrade prompt.
- **Suspend**: non-payment (MoR `past_due`/`canceled` webhook) or abuse →
  revoke tokens + withdraw routes; a grace period before data deletion.

Trials: Pro/Team start with a short trial (MoR-managed). Free never expires but
is reputation-gated and cannot use custom domains until the account is verified.
