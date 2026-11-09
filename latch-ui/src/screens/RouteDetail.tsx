import { useParams, useNavigate } from "react-router-dom";
import { useRoutes, useIPPolicies, useRateLimits, useMutate } from "../api/hooks";
import { api } from "../api/client";
import { useT } from "../i18n";
import { Copy } from "../components/Copy";

export function RouteDetail() {
  const t = useT();
  const { id } = useParams();
  const nav = useNavigate();
  const { data: routes } = useRoutes();
  const { data: policies } = useIPPolicies();
  const { data: limits } = useRateLimits();
  const route = routes?.find((r) => r.id === id);
  const save = useMutate((body: unknown) => api.put(`/routes/${id}`, body), ["routes"]);
  const del = useMutate(() => api.del(`/routes/${id}`), ["routes"]);
  if (!route) return <p className="text-sm">{t("common.loading")}</p>;
  return (
    <div className="space-y-4 max-w-lg">
      <h1 className="text-xl font-semibold">{route.name}</h1>
      <div className="card space-y-2 text-sm">
        <Row k={t("routes.exposure")} v={route.exposure} />
        <Row k={t("common.target")} v={route.target} />
        <Row k={t("common.hostname")} v={route.hostnames.join(", ")} />
        <div className="flex items-center gap-2">
          <button className="btn-ghost" onClick={() => save.mutate({ enabled: !route.enabled })}>
            {route.enabled ? t("common.disabled") : t("common.enabled")}
          </button>
          <Copy text={route.hostnames[0] ? `https://${route.hostnames[0]}` : route.target} />
        </div>
      </div>
      <div className="card space-y-3">
        <h2 className="font-medium">{t("detail.policies")}</h2>
        <div>
          <label className="label">{t("detail.ipPolicy")}</label>
          <select className="input" value={route.ipPolicyId ?? ""} onChange={(e) => save.mutate({ ipPolicyId: e.target.value })}>
            <option value="">{t("common.none")}</option>
            {policies?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </div>
        <div>
          <label className="label">{t("detail.rateLimits")}</label>
          <select className="input" multiple value={route.rateLimitIds ?? []}
            onChange={(e) => save.mutate({ rateLimitIds: Array.from(e.target.selectedOptions).map((o) => o.value) })}>
            {limits?.map((l) => <option key={l.id} value={l.id}>{l.name} ({l.limit}/{l.windowSec}s)</option>)}
          </select>
        </div>
      </div>
      <button className="btn-ghost text-red-600" onClick={() => { if (confirm("Delete?")) { del.mutate(undefined, { onSuccess: () => nav("/routes") }); } }}>
        {t("common.delete")}
      </button>
    </div>
  );
}
function Row({ k, v }: { k: string; v: string }) {
  return <div className="flex justify-between"><span className="text-gray-500">{k}</span><span className="font-mono">{v}</span></div>;
}
