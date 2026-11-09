import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, Me, TunnelToken, Usage } from "../api";
import { Bar, fmtBytes } from "../lib";

export function Overview({ me }: { me: Me }) {
  const usage = useQuery({ queryKey: ["usage"], queryFn: () => api.get<Usage>("/usage") });
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api.get<TunnelToken[]>("/tokens") });
  const active = (tokens.data ?? []).filter((t) => t.status === "active");
  const u = usage.data;

  return (
    <div className="space-y-4">
      <div className="card">
        <h1 className="text-lg font-semibold">Welcome, {me.user.email.split("@")[0]}</h1>
        <p className="text-sm text-gray-500">
          Expose a local port to the internet with valid HTTPS, through our managed relay — no servers to run.
        </p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Link to="/tunnels" className="btn">Create a tunnel</Link>
          <Link to="/download" className="btn-ghost">Download the agent</Link>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-3">
        <Link to="/tunnels" className="card hover:border-brand">
          <div className="text-sm text-gray-500">Active tunnels</div>
          <div className="text-2xl font-semibold">{active.length}{u ? ` / ${u.tokenLimit}` : ""}</div>
        </Link>
        <Link to="/usage" className="card hover:border-brand">
          <div className="text-sm text-gray-500">Bandwidth this month</div>
          <div className="text-2xl font-semibold">{u ? fmtBytes(u.bytesTotal) : "—"}</div>
          {u && <div className="mt-2"><Bar value={u.bytesTotal} max={u.bandwidthLimit} /></div>}
        </Link>
        <Link to="/billing" className="card hover:border-brand">
          <div className="text-sm text-gray-500">Plan</div>
          <div className="text-2xl font-semibold capitalize">{me.account.planId}</div>
          <div className="text-xs text-brand">Manage billing →</div>
        </Link>
      </div>

      {active.length === 0 && (
        <div className="card text-sm text-gray-500">
          You have no tunnels yet. Head to <Link to="/tunnels" className="text-brand">Tunnels</Link> to create your
          first one — it takes about a minute to go live.
        </div>
      )}
    </div>
  );
}
