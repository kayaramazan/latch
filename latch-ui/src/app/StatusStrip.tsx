import { useStatus } from "../api/hooks";
import { useT } from "../i18n";

export function StatusStrip() {
  const t = useT();
  const { data } = useStatus();
  const relays = data?.relays ?? [];
  const outOfSync = relays.some((r) => r.syncState === "out_of_sync");
  return (
    <div className="border-b border-gray-200 dark:border-gray-800 px-6 py-2 flex items-center gap-3 text-xs">
      {relays.length === 0 && <span className="text-gray-500">{t("status.localProxy")}</span>}
      {relays.map((r) => (
        <span key={r.relayId} className="pill bg-gray-100 dark:bg-gray-800">
          <Dot state={r.state} /> {r.relayName} · {r.state}
        </span>
      ))}
      {outOfSync && <span className="pill bg-red-100 text-red-700">{t("status.outOfSync")}</span>}
    </div>
  );
}

function Dot({ state }: { state: string }) {
  const color = state === "connected" ? "bg-green-500" : state === "backoff" ? "bg-amber-500" : "bg-gray-400";
  return <span className={`inline-block w-2 h-2 rounded-full mr-1 ${color}`} />;
}
