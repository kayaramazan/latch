import { useState } from "react";
import { api, Explanation } from "../api/client";
import { useRoutes } from "../api/hooks";
import { useT } from "../i18n";

export function Explain() {
  const t = useT();
  const { data: routes } = useRoutes();
  const [routeId, setRouteId] = useState("");
  const [ip, setIp] = useState("");
  const [path, setPath] = useState("/");
  const [res, setRes] = useState<Explanation | null>(null);
  async function run() {
    setRes(await api.post<Explanation>("/explain", { routeId, clientIp: ip, method: "GET", path }));
  }
  return (
    <div className="space-y-3 max-w-lg">
      <h1 className="text-xl font-semibold">{t("explain.title")}</h1>
      <div className="card space-y-2">
        <div><label className="label">{t("explain.route")}</label>
          <select className="input" value={routeId} onChange={(e) => setRouteId(e.target.value)}>
            <option value="">—</option>{routes?.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
          </select></div>
        <div className="flex gap-2">
          <div className="flex-1"><label className="label">{t("explain.clientIp")}</label><input className="input" value={ip} onChange={(e) => setIp(e.target.value)} /></div>
          <div className="flex-1"><label className="label">{t("explain.path")}</label><input className="input" value={path} onChange={(e) => setPath(e.target.value)} /></div>
        </div>
        <button className="btn" disabled={!routeId} onClick={run}>{t("explain.run")}</button>
      </div>
      {res && (
        <div className="card text-sm space-y-1">
          <p>{res.routeId} ({res.where}), {res.clientIp}{res.country ? ` [${res.country}]` : ""}</p>
          <ul className="font-mono text-xs space-y-0.5">
            {res.steps.map((st, i) => <li key={i} className={st.matched ? "font-semibold" : "text-gray-500"}>{st.matched ? "● " : "○ "}{st.stage}: {st.detail}</li>)}
          </ul>
          <p className="font-medium">{t("explain.result")}: <b className={res.result === "deny" ? "text-red-600" : "text-green-600"}>{res.result.toUpperCase()}</b>{res.denyReason ? ` (${res.denyReason})` : ""}</p>
        </div>
      )}
    </div>
  );
}
