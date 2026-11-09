import { useQuery } from "@tanstack/react-query";
import { api, Usage } from "../api";
import { Bar, fmtBytes } from "../lib";

export function UsageScreen() {
  const usage = useQuery({ queryKey: ["usage"], queryFn: () => api.get<Usage>("/usage") });
  if (usage.isLoading) return <div className="text-sm text-gray-500">Loading…</div>;
  if (!usage.data) return <div className="text-sm text-red-600">Could not load usage.</div>;
  const u = usage.data;

  return (
    <div className="space-y-4">
      <div className="card space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="font-medium">This month</h2>
          <span className="pill bg-brand/10 text-brand">{u.plan.name} plan</span>
        </div>

        <Metric label="Bandwidth" value={fmtBytes(u.bytesTotal)} limit={fmtBytes(u.bandwidthLimit)} bar={[u.bytesTotal, u.bandwidthLimit]} />
        <Metric label="Active tunnels" value={String(u.tokens)} limit={String(u.tokenLimit)} bar={[u.tokens, u.tokenLimit]} />
        <Metric label="Custom domains" value={String(u.domains)} limit={String(u.domainLimit)} bar={[u.domains, u.domainLimit]} />

        <div className="grid grid-cols-2 gap-4 pt-2 text-sm sm:grid-cols-4">
          <Stat label="Requests" value={u.requests.toLocaleString()} />
          <Stat label="In" value={fmtBytes(u.bytesIn)} />
          <Stat label="Out" value={fmtBytes(u.bytesOut)} />
          <Stat label="Req/min limit" value={String(u.plan.requestsPerMin)} />
        </div>
      </div>

      {u.plan.hardCapBandwidth && (
        <div className="card text-sm text-gray-500">
          On the Free plan, traffic is paused when you reach your bandwidth allowance. Upgrade on the
          <b> Billing</b> tab to meter overage instead and lift the cap.
        </div>
      )}
    </div>
  );
}

function Metric({ label, value, limit, bar }: { label: string; value: string; limit: string; bar: [number, number] }) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between text-sm">
        <span>{label}</span>
        <span className="text-gray-500">{value} / {limit}</span>
      </div>
      <Bar value={bar[0]} max={bar[1]} />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-gray-500">{label}</div>
      <div className="text-lg font-semibold">{value}</div>
    </div>
  );
}
