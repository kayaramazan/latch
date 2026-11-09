import { useState } from "react";
import { api, ApiError } from "../api";

export function Auth({ onDone, onBack, initialMode = "login" }: { onDone: () => void; onBack?: () => void; initialMode?: "login" | "signup" }) {
  const [mode, setMode] = useState<"login" | "signup">(initialMode);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [accountName, setAccountName] = useState("");
  const [totp, setTotp] = useState("");
  const [mfa, setMfa] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setErr("");
    setBusy(true);
    try {
      if (mode === "signup") {
        await api.post("/signup", { email, password, accountName });
        onDone();
        return;
      }
      const r = await api.post<{ ok?: boolean; mfaRequired?: boolean }>("/login", {
        email,
        password,
        totp: mfa ? totp : "",
      });
      if (r.mfaRequired) {
        setMfa(true);
        setErr("Enter the 6-digit code from your authenticator app.");
        return;
      }
      onDone();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto mt-16 max-w-sm">
      <div className="mb-6 text-center">
        <div className="text-2xl font-semibold">Latch</div>
        <div className="text-sm text-gray-500">Self-hosted tunnels, managed for you.</div>
      </div>
      <div className="card space-y-3">
        <div className="flex gap-2">
          <button
            className={mode === "login" ? "btn flex-1" : "btn-ghost flex-1"}
            onClick={() => { setMode("login"); setMfa(false); setErr(""); }}
          >
            Sign in
          </button>
          <button
            className={mode === "signup" ? "btn flex-1" : "btn-ghost flex-1"}
            onClick={() => { setMode("signup"); setMfa(false); setErr(""); }}
          >
            Sign up
          </button>
        </div>

        {mode === "signup" && (
          <div>
            <label className="label">Account name</label>
            <input className="input" value={accountName} onChange={(e) => setAccountName(e.target.value)} placeholder="Acme Inc." />
          </div>
        )}
        <div>
          <label className="label">Email</label>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div>
          <label className="label">Password</label>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={mode === "signup" ? "8+ characters" : ""} />
        </div>
        {mfa && (
          <div>
            <label className="label">Authentication code</label>
            <input className="input" value={totp} onChange={(e) => setTotp(e.target.value)} inputMode="numeric" placeholder="123456" />
          </div>
        )}

        {err && <div className="text-sm text-red-600">{err}</div>}

        <button className="btn w-full" disabled={busy || !email || !password} onClick={submit}>
          {busy ? "…" : mode === "signup" ? "Create account" : "Sign in"}
        </button>
        {onBack && (
          <button className="w-full text-center text-xs text-gray-500 hover:text-brand" onClick={onBack}>
            ← Back to home
          </button>
        )}
      </div>
    </div>
  );
}
