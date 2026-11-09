import { useState } from "react";
import { api, ApiError } from "../api";

const FEATURES: [string, string][] = [
  ["Valid HTTPS in minutes", "Expose a local port on a public subdomain with automatic, trusted certificates — no setup."],
  ["Works behind NAT", "The agent dials out to our relay. Nothing needs to be open to the internet on your side."],
  ["Managed for you", "We run the relays and renew certificates. You just pick a port and go."],
];

const STEPS: [string, string][] = [
  ["1 · Create a tunnel", "Name it and get a public URL plus a one-time connection string."],
  ["2 · Run the agent", "Paste the string on the machine you want to expose. One command."],
  ["3 · You're live", "Your service is online at https://your-name.latch.app with valid HTTPS."],
];

const PLANS: { name: string; price: string; lines: string[] }[] = [
  { name: "Free", price: "$0", lines: ["1 tunnel", "5 GB / month", "Community support"] },
  { name: "Pro", price: "$9", lines: ["10 tunnels", "100 GB / month", "3 custom domains"] },
  { name: "Team", price: "$29", lines: ["50 tunnels", "1 TB / month", "25 custom domains"] },
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
    <div>
      <header className="mx-auto flex max-w-5xl items-center justify-between p-4">
        <span className="text-lg font-semibold">Latch</span>
        <div className="flex items-center gap-2">
          <button className="btn-ghost" onClick={onSignIn}>Sign in</button>
          <button className="btn" onClick={onGetStarted}>Get started</button>
        </div>
      </header>

      <section className="mx-auto max-w-3xl px-4 py-16 text-center">
        <h1 className="text-4xl font-bold tracking-tight sm:text-5xl">
          Put any local port online — with real HTTPS.
        </h1>
        <p className="mx-auto mt-4 max-w-xl text-lg text-gray-500">
          Latch gives your machine a public, secure URL through a managed relay. No servers to run,
          no certificates to manage, nothing to open on your firewall.
        </p>
        <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <button className="btn px-5 py-2.5 text-base" disabled={busy} onClick={tryNow}>
            {busy ? "Starting…" : "Try free — no sign-up"}
          </button>
          <button className="btn-ghost px-5 py-2.5 text-base" onClick={onGetStarted}>
            Create an account
          </button>
        </div>
        {err && <div className="mt-3 text-sm text-red-600">{err}</div>}
        <p className="mt-3 text-xs text-gray-400">The free trial runs for 1 hour — keep your work by signing up any time.</p>
      </section>

      <section className="mx-auto grid max-w-5xl gap-4 px-4 pb-8 md:grid-cols-3">
        {FEATURES.map(([t, d]) => (
          <div key={t} className="card">
            <div className="font-medium">{t}</div>
            <p className="mt-1 text-sm text-gray-500">{d}</p>
          </div>
        ))}
      </section>

      <section className="mx-auto max-w-5xl px-4 py-8">
        <h2 className="mb-4 text-center text-xl font-semibold">How it works</h2>
        <div className="grid gap-4 md:grid-cols-3">
          {STEPS.map(([t, d]) => (
            <div key={t} className="card">
              <div className="font-medium text-brand">{t}</div>
              <p className="mt-1 text-sm text-gray-500">{d}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="mx-auto max-w-5xl px-4 py-8">
        <h2 className="mb-4 text-center text-xl font-semibold">Simple pricing</h2>
        <div className="grid gap-4 md:grid-cols-3">
          {PLANS.map((p) => (
            <div key={p.name} className="card">
              <div className="flex items-baseline justify-between">
                <span className="font-semibold">{p.name}</span>
                <span className="text-2xl font-semibold">{p.price}<span className="text-sm font-normal text-gray-500">{p.price !== "$0" ? "/mo" : ""}</span></span>
              </div>
              <ul className="mt-2 space-y-1 text-sm text-gray-600 dark:text-gray-300">
                {p.lines.map((l) => <li key={l}>{l}</li>)}
              </ul>
            </div>
          ))}
        </div>
        <div className="mt-6 text-center">
          <button className="btn px-5 py-2.5 text-base" onClick={onGetStarted}>Get started free</button>
        </div>
      </section>

      <footer className="mx-auto max-w-5xl px-4 py-10 text-center text-xs text-gray-400">
        Latch — self-hosted tunnels, managed for you.
      </footer>
    </div>
  );
}
