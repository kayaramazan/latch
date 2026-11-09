import { useState } from "react";
import { api } from "../api/client";
import { useIPPolicies, useRoutes, useMutate } from "../api/hooks";
import { useT } from "../i18n";

export function Policies() {
  const t = useT();
  const { data: policies } = useIPPolicies();
  const { data: routes } = useRoutes();
  const create = useMutate((b: unknown) => api.post("/ip-policies", b), ["ip-policies"]);
  const del = useMutate((id: string) => api.del(`/ip-policies/${id}`), ["ip-policies"]);
  const [name, setName] = useState("");
  const [cidr, setCidr] = useState("");

  const [route, setRoute] = useState("");
  const [ip, setIp] = useState("");
  const [result, setResult] = useState<string>("");
  async function test() {
    const r = await api.post<{ result: string; denyReason?: string }>("/explain", { routeId: route, clientIp: ip, method: "GET", path: "/" });
    setResult(`${r.result}${r.denyReason ? " (" + r.denyReason + ")" : ""}`);
  }
  return (
    <div className="grid md:grid-cols-2 gap-4">
      <div className="space-y-3">
        <h1 className="text-xl font-semibold">{t("policies.title")}</h1>
        {policies?.map((p) => (
          <div key={p.id} className="card flex items-center justify-between text-sm">
            <div><span className="font-medium">{p.name}</span> <span className="text-gray-500">default {p.defaultAction}, {p.rules.length} rules{p.global ? ", global" : ""}</span></div>
            <button className="btn-ghost text-red-600" onClick={() => del.mutate(p.id)}>{t("common.delete")}</button>
          </div>
        ))}
        <div className="card space-y-2">
          <input className="input" placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
          <input className="input" placeholder="deny CIDR e.g. 203.0.113.0/24" value={cidr} onChange={(e) => setCidr(e.target.value)} />
          <button className="btn" disabled={!name} onClick={() => {
            create.mutate({ name, rules: cidr ? [{ action: "deny", cidr }] : [] }); setName(""); setCidr("");
          }}>{t("common.create")}</button>
        </div>
      </div>
      <div className="card space-y-2 h-fit">
        <h2 className="font-medium">{t("policies.tester")}</h2>
        <select className="input" value={route} onChange={(e) => setRoute(e.target.value)}>
          <option value="">—</option>
          {routes?.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
        </select>
        <input className="input" placeholder={t("policies.testIp")} value={ip} onChange={(e) => setIp(e.target.value)} />
        <button className="btn" disabled={!route || !ip} onClick={test}>{t("policies.run")}</button>
        {result && <p className="text-sm">{t("policies.result")}: <b>{result}</b></p>}
      </div>
    </div>
  );
}
