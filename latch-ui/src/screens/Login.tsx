import { useState } from "react";
import { api, setCsrf } from "../api/client";
import { useT } from "../i18n";

export function Login({ onDone }: { onDone: () => void }) {
  const t = useT();
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      const r = await api.post<{ csrfToken: string }>("/auth/login", { password: pw });
      setCsrf(r.csrfToken);
      onDone();
    } catch {
      setErr(t("login.error"));
    }
  }
  return (
    <div className="min-h-screen flex items-center justify-center">
      <form onSubmit={submit} className="card w-80 space-y-3">
        <h1 className="text-lg font-semibold">{t("login.title")}</h1>
        <div>
          <label className="label">{t("login.password")}</label>
          <input className="input" type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoFocus />
        </div>
        {err && <p className="text-sm text-red-600">{err}</p>}
        <button className="btn w-full justify-center" type="submit">{t("login.submit")}</button>
      </form>
    </div>
  );
}
