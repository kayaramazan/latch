import { Link } from "react-router-dom";
import { useRoutes, useStatus } from "../api/hooks";
import { useT } from "../i18n";

export function RoutesScreen() {
  const t = useT();
  const { data: routes } = useRoutes();
  const { data: status } = useStatus();
  const health = status?.health ?? {};
  const urls: Record<string, string> = {};
  for (const r of status?.relays ?? []) for (const u of r.publicUrls ?? []) urls[u] = u;
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("routes.title")}</h1>
        <Link className="btn" to="/routes/new">{t("routes.add")}</Link>
      </div>
      {routes?.length === 0 && <p className="text-sm text-gray-500">{t("routes.empty")}</p>}
      <div className="space-y-2">
        {routes?.map((r) => (
          <Link to={`/routes/${r.id}`} key={r.id} className="card flex items-center gap-3 hover:border-brand">
            <HealthDot state={health[r.id]} enabled={r.enabled} />
            <div className="flex-1 min-w-0">
              <div className="font-medium truncate">{r.name}</div>
              <div className="text-xs text-gray-500 truncate">
                {r.kind === "tcp" ? `tcp/${r.hostnames?.[0] ?? ""}` : r.hostnames.join(", ")} → {r.target}
              </div>
            </div>
            <span className="pill bg-gray-100 dark:bg-gray-800">{t("routes.exposure")}: {r.exposure}</span>
          </Link>
        ))}
      </div>
    </div>
  );
}

function HealthDot({ state, enabled }: { state?: string; enabled: boolean }) {
  if (!enabled) return <span className="w-2.5 h-2.5 rounded-full bg-gray-300" title="disabled" />;
  const c = state === "healthy" ? "bg-green-500" : state === "unhealthy" ? "bg-red-500" : "bg-gray-400";
  return <span className={`w-2.5 h-2.5 rounded-full ${c}`} title={state ?? "unknown"} />;
}
