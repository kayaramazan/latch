import { useEffect, useRef, useState } from "react";
import { AccessEvent } from "../api/client";
import { useT } from "../i18n";

export function Live() {
  const t = useT();
  const [events, setEvents] = useState<AccessEvent[]>([]);
  const [paused, setPaused] = useState(false);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    const es = new EventSource("/api/events/stream");
    es.onmessage = (m) => {
      if (pausedRef.current) return;
      try {
        const ev = JSON.parse(m.data) as AccessEvent;
        setEvents((prev) => [ev, ...prev].slice(0, 200));
      } catch { /* ignore */ }
    };
    return () => es.close();
  }, []);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("live.title")}</h1>
        <button className="btn-ghost" onClick={() => setPaused((p) => !p)}>{paused ? t("live.resume") : t("live.pause")}</button>
      </div>
      <div className="card overflow-x-auto">
        <table className="w-full text-xs">
          <thead className="text-gray-500 text-left">
            <tr><th className="py-1">time</th><th>{t("live.source")}</th><th>host</th><th>ip</th><th>method</th><th>path</th><th>status</th><th>decision</th></tr>
          </thead>
          <tbody>
            {events.map((e, i) => (
              <tr key={i} className={e.decision === "deny" ? "text-red-600" : ""}>
                <td className="py-0.5 whitespace-nowrap">{new Date(e.ts).toLocaleTimeString()}</td>
                <td>{e.source}</td><td className="truncate max-w-[10rem]">{e.hostname}</td>
                <td>{e.clientIp}{e.country ? ` ${e.country}` : ""}</td><td>{e.method}</td>
                <td className="truncate max-w-[12rem]">{e.path}</td><td>{e.status}</td>
                <td>{e.decision === "deny" ? `${t("decision.deny")}: ${e.denyReason}` : t("decision.allow")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
