import { useState } from "react";
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from "recharts";
import { useDashboard } from "../api/hooks";
import { useT } from "../i18n";

export function Dashboard() {
  const t = useT();
  const [win, setWin] = useState(3600);
  const { data } = useDashboard(win);
  const statusData = Object.entries(data?.statusClasses ?? {}).map(([name, value]) => ({ name, value }));
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("dashboard.title")}</h1>
        <select className="input w-36" value={win} onChange={(e) => setWin(Number(e.target.value))}>
          <option value={900}>15m</option>
          <option value={3600}>1h</option>
          <option value={86400}>24h</option>
        </select>
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Stat label={t("dashboard.requestsPerSec")} value={(data?.requestsPerSec ?? 0).toFixed(2)} />
        <Stat label={t("dashboard.denied")} value={String(data?.denied ?? 0)} />
        <Stat label="p95" value={`${Math.round(data?.p95Ms ?? 0)}ms`} />
        <Stat label="p99" value={`${Math.round(data?.p99Ms ?? 0)}ms`} />
      </div>
      <div className="card">
        <h2 className="text-sm font-medium mb-2">{t("dashboard.statusMix")}</h2>
        <div style={{ height: 180 }}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={statusData}>
              <XAxis dataKey="name" fontSize={12} /><YAxis fontSize={12} allowDecimals={false} />
              <Tooltip /><Bar dataKey="value" fill="#2563eb" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>
      <div className="grid md:grid-cols-2 gap-3">
        <div className="card">
          <h2 className="text-sm font-medium mb-2">{t("dashboard.topRoutes")}</h2>
          <ul className="text-sm space-y-1">
            {(data?.topRoutes ?? []).map((r) => (
              <li key={r.routeId} className="flex justify-between"><span>{r.hostname || r.routeId}</span><span>{r.count}</span></li>
            ))}
          </ul>
        </div>
        <div className="card">
          <h2 className="text-sm font-medium mb-2">{t("dashboard.topBlocked")}</h2>
          <ul className="text-sm space-y-1">
            {(data?.topBlockedIpsLastHour ?? []).map((r) => (
              <li key={r.clientIp} className="flex justify-between"><span>{r.clientIp}</span><span>{r.count}</span></li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return <div className="card"><div className="text-xs text-gray-500">{label}</div><div className="text-2xl font-semibold">{value}</div></div>;
}
