# UI screens

All screens are in `latch-ui`, served by `latchd`. Default language English,
Turkish included; every string goes through i18n keys. The product rule from
SPEC: adding a route asks exactly two questions; everything else has a
sensible default and lives behind "Advanced".

## Navigation

Sidebar: **Dashboard · Routes · Policies · Rate limits · Relays · Live ·
Logs · History · Settings**. A persistent status strip shows: local proxy
state, each relay's connection state, and a red "relay out of sync" or
"config apply failed" badge that links to the cause.

## Screens

| # | Screen | Purpose | Main actions |
|---|--------|---------|--------------|
| 1 | **First run / Login** | Set password if listen is non-loopback or user wants one; language pick. | Set password, continue |
| 2 | **Dashboard** | Health at a glance: req/s, p50/p95/p99, status code mix, per-route traffic, tunnel health and reconnect counts, top blocked. | Time window (15m/1h/24h), click-through to route/live |
| 3 | **Routes** | List with status colours: health (green/red/grey), tunnel connection, TLS state, sync. Public URL with copy button. | Add route, enable/disable toggle, open detail |
| 4 | **Add route** (wizard, 2 steps) | Step 1 "What are you publishing?": local port or `host:port` (auto-detects listening ports and suggests). Step 2 "How should it be reached?": **Tunnel** (pick relay → generated subdomain, editable, or custom domain with CNAME instructions) or **Local** (enter domain; DNS check against this machine's public IP). Preview of resulting URL. | Create; "Advanced" expander: path prefix, headers, TLS mode, health check, policies |
| 5 | **Route detail** | Tabs: Overview (status, URLs, target, health history), Policies (attach IP policy / rate limits), Headers, Health check, TLS, Danger zone. | Edit, apply with affected-routes confirmation, delete |
| 6 | **IP policies** | List + editor: default action, ordered rules (CIDR or country), deny behaviour, global toggle. Right pane "Does this IP pass?" tester with country lookup. | Create, edit, test, attach to routes |
| 7 | **Rate limits** | List + editor: keys, limit/window, exceed behaviour, global toggle. Live panel: currently limited keys and top limited in last hour, split by where (relay/agent). | Create, edit, attach |
| 8 | **Relays** | Cards per relay: host, state, uptime, RTT, reconnects, protocol/relay version, scope, sync status with "Resync". | **Connect to a relay** (paste `latch://` string), **Set up your relay** wizard, reconnect, remove |
| 9 | **Set up your relay** (wizard) | Collect domain, email, DNS provider; show DNS records to create; generate the one-line install command; DNS check button; final step pastes the connection string back. | Copy command, check DNS, finish |
| 10 | **Live** | Real-time request stream table: time, route, source (local/relay name), IP + country flag, method, path, status, duration, target, deny reason. Filters and pause. Row click → explain for that request. | Filter, pause, export CSV |
| 11 | **Logs** | System log search: level, time range, text. | Filter, download |
| 12 | **History** | Config versions with summary/author; diff view between two versions; rollback with affected-routes preview. Backup export and restore import. | Diff, rollback, export, import |
| 13 | **Settings** | Listen address & auth, trusted proxies, local-mode ports/ACME e-mail/CA, GeoIP database upload, event retention, log level, language. Mutations here show a warning when they restart listeners. | Save |
| 14 | **Explain** | Form: route, client IP, path, headers → ordered evaluation with where each rule runs (relay vs agent) and the outcome. Also reachable from Live rows. | Run |
| 15 | **Apply confirmation** (dialog, everywhere) | Shows validation result and affected routes before any config write; shows field-level errors inline on failure; on `apply_failed` explains that the previous config is still running. | Apply, cancel |

## Flows that must be fast

- **Share port 3000 over a tunnel** (SPEC success criterion): Routes → Add →
  `3000` → Tunnel → relay → suggested subdomain → Create → URL shown with
  HTTPS and copy button, status turns green when tunnel + health are OK.
  Target: under 60 seconds inside the UI.
- **Block an IP**: Policies → New → add deny rule → attach to route (or
  global) → Apply → Live shows the denied request with `where: relay`.
- **Publish a domain in local mode**: Routes → Add → `127.0.0.1:8080` →
  Local → `site.example.com` → DNS check passes → Create → TLS state goes
  `pending` → `active`.

## Non-functional

- Works at 1024px wide and above; no mobile layout in v1 (read-only pages
  should still render on a phone).
- Every destructive action confirms; every apply shows affected routes.
- Empty states teach: an empty Relays page explains the two options
  (connect / set up); an empty Routes page offers "Share a port".
- Playwright covers flow "Share port 3000 over a tunnel" against an
  in-process relay.
