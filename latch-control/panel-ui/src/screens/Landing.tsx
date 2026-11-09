import { useState } from "react";
import { api, ApiError } from "../api";
import { Copy } from "../lib";
import { INSTALL, RELEASES_URL, detectOS, OS_LABEL } from "../download";

/* Minimal inline icon set (heroicons-style outlines). */
const I = {
  shield: "M12 3l7 4v5c0 4-3 7-7 9-4-2-7-5-7-9V7l7-4z M9 12l2 2 4-4",
  globe: "M12 3a9 9 0 100 18 9 9 0 000-18z M3 12h18 M12 3c2.6 2.7 2.6 15.3 0 18 M12 3c-2.6 2.7-2.6 15.3 0 18",
  bolt: "M13 3L4 14h6l-1 7 9-11h-6l1-7z",
  server: "M4 5h16v5H4z M4 14h16v5H4z M8 7.5h.01 M8 16.5h.01",
  chart: "M4 5v14h16 M8 16v-4 M12 16V8 M16 16v-7",
  lock: "M6 11h12v9H6z M9 11V8a3 3 0 016 0v3",
  code: "M8 9l-4 3 4 3 M16 9l4 3-4 3 M13 6l-2 12",
  plug: "M9 3v4 M15 3v4 M7 7h10v3a5 5 0 01-10 0V7z M12 15v6",
  key: "M15 7a4 4 0 11-5.2 3.8L4 16.6V20h3.4l.9-.9H10v-2h2l1.8-1.8A4 4 0 0015 7z",
  dollar: "M12 3v18 M16 7c0-1.7-1.8-3-4-3s-4 1.3-4 3 1.8 3 4 3 4 1.3 4 3-1.8 3-4 3-4-1.3-4-3",
  refresh: "M4 12a8 8 0 0114-5l2 2 M4 8V4m0 4h4 M20 12a8 8 0 01-14 5l-2-2 M20 16v4m0-4h-4",
  window: "M4 5h16v14H4z M4 9h16 M7 7h.01",
  check: "M5 13l4 4L19 7",
};

function Icon({ d, className = "h-5 w-5" }: { d: string; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round">
      {d.split(" M").map((seg, i) => <path key={i} d={(i ? "M" : "") + seg} />)}
    </svg>
  );
}

const FEATURES: { icon: string; title: string; desc: string }[] = [
  { icon: I.shield, title: "Automatic, valid HTTPS", desc: "Trusted TLS certificates issued and renewed for you. No Let's Encrypt wrangling, no self-signed warnings." },
  { icon: I.plug, title: "Works behind NAT & firewalls", desc: "The agent dials out to the relay. Nothing needs to be exposed or port-forwarded on your side." },
  { icon: I.server, title: "Self-hosted or managed", desc: "Run your own relay on your VPS (open source), or use our managed relays. Same agent, same UI." },
  { icon: I.window, title: "Reverse proxy mode too", desc: "On a public-IP box, Latch is a full reverse proxy with embedded Caddy — domain → upstream, auto HTTPS." },
  { icon: I.globe, title: "Bring your own domain", desc: "Point a custom domain at a tunnel; certificates are provisioned on demand automatically." },
  { icon: I.shield, title: "Access policies", desc: "IP allow / deny lists, country rules via GeoIP, and per-route rate limiting — enforced at the edge." },
  { icon: I.chart, title: "Real observability", desc: "Live request metrics, p50/p95/p99 latency, status mix, top routes and blocked IPs, Prometheus export." },
  { icon: I.bolt, title: "Fast & lightweight", desc: "TLS 1.3 + multiplexed streams. Thousands of requests per second with single-digit-millisecond overhead." },
  { icon: I.lock, title: "Hardened by design", desc: "TLS verification never skipped, argon2id-hashed secrets, fuzzed protocol parsers, no admin ports exposed." },
  { icon: I.window, title: "Manage from a dashboard", desc: "Open ports, add relays, see traffic — all from the UI. No config files, no terminal required." },
  { icon: I.key, title: "2FA, API keys, teams", desc: "TOTP two-factor, API keys for CI/automation, and tenant isolation for every account." },
  { icon: I.code, title: "Open source (Apache-2.0)", desc: "The whole stack is open. Self-host it, audit it, extend it — no lock-in." },
];

const STATS: [string, string][] = [
  ["~2 min", "to go live with HTTPS"],
  ["0", "ports to open inbound"],
  ["3", "OSes — macOS, Linux, Windows"],
  ["100%", "open source"],
];

const COMPARE: { label: string; latch: boolean; diy: boolean; saas: boolean }[] = [
  { label: "Valid HTTPS with zero setup", latch: true, diy: false, saas: true },
  { label: "Works behind NAT / firewall", latch: true, diy: false, saas: true },
  { label: "Run on your own infrastructure", latch: true, diy: true, saas: false },
  { label: "No third-party sees your traffic", latch: true, diy: true, saas: false },
  { label: "Web dashboard, no config files", latch: true, diy: false, saas: true },
  { label: "IP / GeoIP / rate-limit policies", latch: true, diy: false, saas: false },
  { label: "Open source, no lock-in", latch: true, diy: true, saas: false },
];

const PLANS: { name: string; price: string; lines: string[]; featured?: boolean }[] = [
  { name: "Free", price: "$0", lines: ["1 tunnel", "5 GB / month", "Automatic HTTPS", "Community support"] },
  { name: "Pro", price: "$9", lines: ["10 tunnels", "100 GB / month", "3 custom domains", "Priority relays"], featured: true },
  { name: "Team", price: "$29", lines: ["50 tunnels", "1 TB / month", "25 custom domains", "Team members & API"] },
];

export function Landing({
  onSignIn,
  onGetStarted,
  onDone,
}: {
  onSignIn: () => void;
  onGetStarted: () => void;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function tryNow() {
    setBusy(true);
    setErr("");
    try {
      await api.post("/trial", {});
      onDone();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Could not start a trial");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="overflow-x-hidden">
      {/* Header */}
      <header className="sticky top-0 z-20 border-b border-gray-200/60 bg-white/70 glass dark:border-gray-800/60 dark:bg-gray-950/70">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <div className="flex items-center gap-2 font-semibold">
            <span className="icon-chip h-8 w-8"><Icon d={I.bolt} className="h-4 w-4" /></span>
            Latch
          </div>
          <div className="flex items-center gap-2">
            <a className="hidden text-sm text-gray-600 hover:text-brand sm:inline dark:text-gray-300" href="#install">Download</a>
            <button className="btn-ghost" onClick={onSignIn}>Sign in</button>
            <button className="btn" onClick={onGetStarted}>Get started</button>
          </div>
        </div>
      </header>

      {/* Hero */}
      <section className="relative isolate overflow-hidden">
        <div className="blob animate-float" style={{ top: "-6rem", left: "10%", width: "22rem", height: "22rem", background: "#2563eb" }} />
        <div className="blob animate-float" style={{ top: "2rem", right: "6%", width: "20rem", height: "20rem", background: "#7c3aed", animationDelay: "1.5s" }} />
        <div className="absolute inset-0 grid-bg" />
        <div className="relative mx-auto max-w-4xl px-4 py-24 text-center fadeup">
          <span className="pill border border-brand/30 bg-brand/10 text-brand">Self-hosted tunnels · managed for you</span>
          <h1 className="mt-5 text-4xl font-extrabold leading-tight tracking-tight sm:text-6xl">
            Put any local port <span className="gradient-text">online, securely</span>.
          </h1>
          <p className="mx-auto mt-5 max-w-2xl text-lg text-gray-600 dark:text-gray-300">
            Latch gives your machine a public URL with real HTTPS through a relay you own or we host.
            No servers to run, no certificates to manage, nothing to open on your firewall.
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <button className="btn px-6 py-3 text-base" disabled={busy} onClick={tryNow}>
              {busy ? "Starting…" : "Try free — no sign-up"}
            </button>
            <button className="btn-ghost px-6 py-3 text-base" onClick={onGetStarted}>Create an account</button>
          </div>
          {err && <div className="mt-3 text-sm text-red-600">{err}</div>}
          <div className="mt-6 flex flex-wrap items-center justify-center gap-x-5 gap-y-2 text-sm text-gray-500">
            {["No credit card", "Live in ~2 minutes", "Open source"].map((t) => (
              <span key={t} className="inline-flex items-center gap-1"><Icon d={I.check} className="h-4 w-4 text-green-500" /> {t}</span>
            ))}
          </div>

          {/* Browser mock */}
          <div className="mx-auto mt-14 max-w-2xl rounded-xl border border-gray-200 bg-white text-left shadow-xl dark:border-gray-800 dark:bg-gray-900">
            <div className="flex items-center gap-1.5 border-b border-gray-100 px-4 py-2.5 dark:border-gray-800">
              <span className="h-3 w-3 rounded-full bg-red-400" />
              <span className="h-3 w-3 rounded-full bg-yellow-400" />
              <span className="h-3 w-3 rounded-full bg-green-400" />
              <span className="ml-3 inline-flex items-center gap-1 rounded-md bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800">
                <Icon d={I.lock} className="h-3 w-3 text-green-500" /> https://app.latch.app
              </span>
            </div>
            <pre className="block m-0 rounded-none border-0 bg-transparent p-4 leading-relaxed">
{`$ latchd route add --target 127.0.0.1:3000 \\
      --tunnel my-relay --hostname app.latch.app

`}<span className="text-green-500">✓ live</span>{`   https://app.latch.app  →  127.0.0.1:3000
    valid HTTPS · behind NAT · 7ms overhead`}
            </pre>
          </div>
        </div>
      </section>

      {/* Stats */}
      <section className="border-y border-gray-200 bg-gray-50/60 dark:border-gray-800 dark:bg-gray-900/40">
        <div className="mx-auto grid max-w-5xl grid-cols-2 gap-6 px-4 py-10 md:grid-cols-4">
          {STATS.map(([n, l]) => (
            <div key={l} className="text-center">
              <div className="text-3xl font-bold gradient-text">{n}</div>
              <div className="mt-1 text-sm text-gray-500">{l}</div>
            </div>
          ))}
        </div>
      </section>

      {/* Features */}
      <section className="mx-auto max-w-6xl px-4 py-20">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold">Everything you need to ship a port</h2>
          <p className="mt-3 text-gray-500">One tool for tunnels and reverse-proxying — with security and observability built in.</p>
        </div>
        <div className="mt-12 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {FEATURES.map((f) => (
            <div key={f.title} className="card transition hover:-translate-y-0.5 hover:border-brand/50 hover:shadow-md">
              <span className="icon-chip"><Icon d={f.icon} /></span>
              <div className="mt-3 font-semibold">{f.title}</div>
              <p className="mt-1 text-sm text-gray-500">{f.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Two ways to run */}
      <section className="border-y border-gray-200 bg-gray-50/60 px-4 py-20 dark:border-gray-800 dark:bg-gray-900/40">
        <div className="mx-auto max-w-5xl">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-3xl font-bold">Two ways to run it</h2>
            <p className="mt-3 text-gray-500">Start managed in seconds, or self-host for full control. Switch any time.</p>
          </div>
          <div className="mt-10 grid gap-5 md:grid-cols-2">
            <div className="card border-brand/40">
              <div className="flex items-center gap-2">
                <span className="icon-chip"><Icon d={I.bolt} /></span>
                <h3 className="text-lg font-semibold">Managed</h3>
              </div>
              <p className="mt-2 text-sm text-gray-500">We run the relays, issue certificates and handle scaling. Sign up, create a tunnel, download the agent — done.</p>
              <ul className="mt-3 space-y-1.5 text-sm">
                {["Zero infrastructure", "Automatic wildcard certificates", "Usage dashboard & billing"].map((t) => (
                  <li key={t} className="flex items-center gap-2"><Icon d={I.check} className="h-4 w-4 text-green-500" /> {t}</li>
                ))}
              </ul>
            </div>
            <div className="card">
              <div className="flex items-center gap-2">
                <span className="icon-chip"><Icon d={I.server} /></span>
                <h3 className="text-lg font-semibold">Self-hosted</h3>
              </div>
              <p className="mt-2 text-sm text-gray-500">Run the relay on your own VPS. Your traffic never touches a third party, and the whole stack is open source.</p>
              <ul className="mt-3 space-y-1.5 text-sm">
                {["Your infrastructure, your data", "No per-GB fees", "Apache-2.0 licensed"].map((t) => (
                  <li key={t} className="flex items-center gap-2"><Icon d={I.check} className="h-4 w-4 text-green-500" /> {t}</li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      </section>

      {/* How it works */}
      <section className="mx-auto max-w-5xl px-4 py-20">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold">Live in three steps</h2>
        </div>
        <div className="mt-10 grid gap-5 md:grid-cols-3">
          {[
            ["Create a tunnel", "Name it and get a public URL plus a one-time connection string."],
            ["Run the agent", "Paste the string on the machine you want to expose. One command."],
            ["You're online", "Your service is live with valid HTTPS, reachable from anywhere."],
          ].map(([t, d], i) => (
            <div key={t} className="card">
              <div className="text-sm font-semibold text-brand">Step {i + 1}</div>
              <div className="mt-1 font-medium">{t}</div>
              <p className="mt-1 text-sm text-gray-500">{d}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Install */}
      <section id="install" className="mx-auto max-w-4xl px-4 py-20">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold">Install in one line</h2>
          <p className="mt-3 text-gray-500">We detected <b>{OS_LABEL[detectOS()]}</b>. Install the agent, then connect from your dashboard.</p>
        </div>
        <div className="mx-auto mt-8 max-w-2xl space-y-4">
          <div className="card space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">macOS / Linux</span>
              <Copy text={INSTALL.unix} label="Copy" />
            </div>
            <code className="code block rounded bg-gray-100 p-3 dark:bg-gray-800">{INSTALL.unix}</code>
          </div>
          <div className="card space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Docker</span>
              <Copy text={INSTALL.docker} label="Copy" />
            </div>
            <code className="code block rounded bg-gray-100 p-3 dark:bg-gray-800">{INSTALL.docker}</code>
          </div>
          <div className="flex flex-wrap items-center justify-center gap-3 pt-2 text-sm">
            <a className="btn-ghost" href={RELEASES_URL} target="_blank" rel="noopener">Windows & all downloads ↗</a>
            <span className="text-gray-400">Signed binaries · macOS, Linux, Windows · amd64 & arm64</span>
          </div>
        </div>
      </section>

      {/* Comparison */}
      <section className="border-y border-gray-200 bg-gray-50/60 px-4 py-20 dark:border-gray-800 dark:bg-gray-900/40">
        <div className="mx-auto max-w-3xl">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-3xl font-bold">How Latch compares</h2>
          </div>
          <div className="mt-10 overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
            <div className="grid grid-cols-4 border-b border-gray-200 bg-gray-50 text-sm font-medium dark:border-gray-800 dark:bg-gray-900/60">
              <div className="p-3" />
              <div className="p-3 text-center text-brand">Latch</div>
              <div className="p-3 text-center text-gray-500">DIY proxy</div>
              <div className="p-3 text-center text-gray-500">Closed SaaS</div>
            </div>
            {COMPARE.map((row) => (
              <div key={row.label} className="grid grid-cols-4 items-center border-b border-gray-100 text-sm last:border-0 dark:border-gray-800">
                <div className="p-3 text-gray-600 dark:text-gray-300">{row.label}</div>
                <Cell on={row.latch} />
                <Cell on={row.diy} />
                <Cell on={row.saas} />
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Pricing */}
      <section className="mx-auto max-w-5xl px-4 py-20">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold">Simple, honest pricing</h2>
          <p className="mt-3 text-gray-500">Start free. Upgrade when you grow. Self-host for nothing.</p>
        </div>
        <div className="mt-12 grid gap-5 md:grid-cols-3">
          {PLANS.map((p) => (
            <div key={p.name} className={`card relative ${p.featured ? "border-brand shadow-lg md:-translate-y-2" : ""}`}>
              {p.featured && <span className="pill absolute -top-3 left-1/2 -translate-x-1/2 bg-brand text-white">Most popular</span>}
              <div className="flex items-baseline justify-between">
                <span className="font-semibold">{p.name}</span>
                <span className="text-3xl font-bold">{p.price}<span className="text-sm font-normal text-gray-500">{p.price !== "$0" ? "/mo" : ""}</span></span>
              </div>
              <ul className="mt-4 space-y-2 text-sm">
                {p.lines.map((l) => <li key={l} className="flex items-center gap-2"><Icon d={I.check} className="h-4 w-4 text-green-500" /> {l}</li>)}
              </ul>
              <button className={`mt-5 w-full ${p.featured ? "btn" : "btn-ghost"}`} onClick={onGetStarted}>
                {p.price === "$0" ? "Start free" : `Choose ${p.name}`}
              </button>
            </div>
          ))}
        </div>
      </section>

      {/* Final CTA */}
      <section className="px-4 pb-24">
        <div className="relative mx-auto max-w-4xl overflow-hidden rounded-2xl p-12 text-center text-white shadow-xl" style={{ background: "linear-gradient(120deg,#2563eb,#7c3aed 55%,#0ea5e9)" }}>
          <h2 className="text-3xl font-bold">Your port, online in two minutes.</h2>
          <p className="mx-auto mt-3 max-w-xl text-white/90">No credit card. No config files. Try it right now as a guest, or create an account to keep your tunnels.</p>
          <div className="mt-7 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <button className="rounded-md bg-white px-6 py-3 text-base font-semibold text-brand hover:opacity-90 disabled:opacity-60" disabled={busy} onClick={tryNow}>
              {busy ? "Starting…" : "Try free — no sign-up"}
            </button>
            <button className="rounded-md border border-white/70 px-6 py-3 text-base font-semibold hover:bg-white/10" onClick={onGetStarted}>
              Create an account
            </button>
          </div>
        </div>
      </section>

      <footer className="border-t border-gray-200 px-4 py-10 text-center text-sm text-gray-400 dark:border-gray-800">
        <div className="mx-auto max-w-5xl">
          Latch — self-hosted tunnels + reverse proxy, managed for you. Apache-2.0.
        </div>
      </footer>
    </div>
  );
}

function Cell({ on }: { on: boolean }) {
  return (
    <div className="flex items-center justify-center p-3">
      {on ? (
        <Icon d={I.check} className="h-5 w-5 text-green-500" />
      ) : (
        <span className="text-gray-300 dark:text-gray-600">—</span>
      )}
    </div>
  );
}
