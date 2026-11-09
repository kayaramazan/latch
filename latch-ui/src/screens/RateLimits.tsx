import { useState } from "react";
import { api } from "../api/client";
import { useRateLimits, useMutate } from "../api/hooks";
import { useT } from "../i18n";

export function RateLimits() {
  const t = useT();
  const { data } = useRateLimits();
  const create = useMutate((b: unknown) => api.post("/rate-limits", b), ["rate-limits"]);
  const del = useMutate((id: string) => api.del(`/rate-limits/${id}`), ["rate-limits"]);
  const [name, setName] = useState("");
  const [limit, setLimit] = useState(60);
  const [win, setWin] = useState(60);
  return (
    <div className="space-y-3 max-w-lg">
      <h1 className="text-xl font-semibold">{t("ratelimits.title")}</h1>
      {data?.map((r) => (
        <div key={r.id} className="card flex items-center justify-between text-sm">
          <div><span className="font-medium">{r.name}</span> <span className="text-gray-500">{r.limit}/{r.windowSec}s by {r.keys.join(",")}</span></div>
          <button className="btn-ghost text-red-600" onClick={() => del.mutate(r.id)}>{t("common.delete")}</button>
        </div>
      ))}
      <div className="card space-y-2">
        <input className="input" placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
        <div className="flex gap-2">
          <div className="flex-1"><label className="label">{t("ratelimits.limit")}</label><input className="input" type="number" value={limit} onChange={(e) => setLimit(Number(e.target.value))} /></div>
          <div className="flex-1"><label className="label">{t("ratelimits.window")}</label><input className="input" type="number" value={win} onChange={(e) => setWin(Number(e.target.value))} /></div>
        </div>
        <button className="btn" disabled={!name} onClick={() => { create.mutate({ name, keys: ["ip"], limit, windowSec: win }); setName(""); }}>{t("common.create")}</button>
      </div>
    </div>
  );
}
