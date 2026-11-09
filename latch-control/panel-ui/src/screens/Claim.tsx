import { useEffect, useState } from "react";
import { api, ApiError } from "../api";

function remaining(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "expired";
  const m = Math.floor(ms / 60000);
  const h = Math.floor(m / 60);
  return h > 0 ? `${h}h ${m % 60}m` : `${m}m`;
}

export function TrialBanner({ expiresAt, onClaimed }: { expiresAt: string; onClaimed: () => void }) {
  const [open, setOpen] = useState(false);
  const [, setTick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(t);
  }, []);

  return (
    <div className="mb-4 rounded-lg border border-brand bg-brand/5 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm">
          <b>Guest trial</b> — expires in {remaining(expiresAt)}. Save your account to keep your tunnels.
        </div>
        <button className="btn" onClick={() => setOpen((v) => !v)}>Save my account</button>
      </div>
      {open && <ClaimForm onClaimed={onClaimed} />}
    </div>
  );
}

function ClaimForm({ onClaimed }: { onClaimed: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setErr("");
    try {
      await api.post("/claim", { email, password });
      onClaimed();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Could not save account");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-3 flex flex-wrap items-end gap-2 border-t border-brand/30 pt-3">
      <div className="flex-1 min-w-48">
        <label className="label">Email</label>
        <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="flex-1 min-w-48">
        <label className="label">Password</label>
        <input className="input" type="password" value={password} placeholder="8+ characters" onChange={(e) => setPassword(e.target.value)} />
      </div>
      <button className="btn" disabled={busy || !email || !password} onClick={submit}>
        {busy ? "…" : "Save account"}
      </button>
      {err && <div className="w-full text-sm text-red-600">{err}</div>}
    </div>
  );
}
