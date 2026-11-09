import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../api/client";
import { useRelays, useLocalPorts } from "../api/hooks";
import { useT } from "../i18n";

export function AddRoute() {
  const t = useT();
  const nav = useNavigate();
  const { data: relays } = useRelays();
  const { data: ports } = useLocalPorts();
  const [step, setStep] = useState(1);
  const [target, setTarget] = useState("");
  const [mode, setMode] = useState<"tunnel" | "local">("tunnel");
  const [relayId, setRelayId] = useState("");
  const [hostname, setHostname] = useState("");
  const [err, setErr] = useState<string>("");
  const [fieldErrs, setFieldErrs] = useState<Record<string, string>>({});

  const relay = relays?.find((r) => r.id === relayId) ?? relays?.[0];
  const previewUrl = hostname ? `https://${hostname}` : "";

  async function create() {
    setErr(""); setFieldErrs({});
    const body: Record<string, unknown> = {
      name: hostname || target, target, kind: "http",
      exposure: mode, hostnames: [hostname],
    };
    if (mode === "tunnel") body.relayId = relay?.id;
    try {
      const r = await api.post<{ id: string }>("/routes", body);
      nav(`/routes/${r.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) {
        const fe: Record<string, string> = {};
        for (const f of e.fields) fe[f.path.split(".").pop() || f.path] = f.message;
        setFieldErrs(fe);
      }
      setErr(e instanceof Error ? e.message : "error");
    }
  }

  return (
    <div className="max-w-lg space-y-4" data-testid="add-route">
      <h1 className="text-xl font-semibold">{t("addroute.title")}</h1>

      {step === 1 && (
        <div className="card space-y-3">
          <h2 className="font-medium">{t("addroute.step1")}</h2>
          <div>
            <label className="label">{t("common.target")}</label>
            <input className="input" placeholder="127.0.0.1:3000" value={target}
              onChange={(e) => setTarget(e.target.value)} data-testid="target" autoFocus />
            <p className="text-xs text-gray-500 mt-1">{t("addroute.targetHelp")}</p>
            {ports && ports.length > 0 && (
              <div className="mt-2">
                <p className="text-xs text-gray-500 mb-1">{t("addroute.detected")}</p>
                <div className="flex flex-wrap gap-1.5" data-testid="port-suggestions">
                  {ports.map((p) => (
                    <button key={p.port} type="button"
                      className="pill border border-gray-300 dark:border-gray-700 hover:border-brand"
                      onClick={() => setTarget(p.address)} data-testid={`port-${p.port}`}>
                      :{p.port}{p.process ? ` ${p.process}` : ""}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>
          <div className="flex justify-end">
            <button className="btn" disabled={!target} onClick={() => setStep(2)} data-testid="next">{t("common.next")}</button>
          </div>
        </div>
      )}

      {step === 2 && (
        <div className="card space-y-3">
          <h2 className="font-medium">{t("addroute.step2")}</h2>
          <div className="flex gap-2">
            <Choice active={mode === "tunnel"} onClick={() => setMode("tunnel")} label={t("addroute.tunnel")} testid="mode-tunnel" />
            <Choice active={mode === "local"} onClick={() => setMode("local")} label={t("addroute.local")} testid="mode-local" />
          </div>
          {mode === "tunnel" && (
            <div>
              <label className="label">{t("addroute.pickRelay")}</label>
              <select className="input" value={relay?.id ?? ""} onChange={(e) => setRelayId(e.target.value)} data-testid="relay">
                {relays?.map((r) => <option key={r.id} value={r.id}>{r.name} ({r.host})</option>)}
              </select>
            </div>
          )}
          <div>
            <label className="label">{t("addroute.hostname")}</label>
            <input className="input" placeholder={relay?.baseDomain ? `app.${relay.baseDomain}` : "app.example.com"}
              value={hostname} onChange={(e) => setHostname(e.target.value)} data-testid="hostname" />
            {fieldErrs.hostnames && <p className="text-xs text-red-600 mt-1">{fieldErrs.hostnames}</p>}
            {fieldErrs["hostnames[0]"] && <p className="text-xs text-red-600 mt-1">{fieldErrs["hostnames[0]"]}</p>}
          </div>
          {previewUrl && <p className="text-sm">{t("addroute.preview")} <code className="font-mono">{previewUrl}</code></p>}
          {err && !Object.keys(fieldErrs).length && <p className="text-sm text-red-600">{err}</p>}
          <div className="flex justify-between">
            <button className="btn-ghost" onClick={() => setStep(1)}>{t("common.back")}</button>
            <button className="btn" disabled={!hostname || (mode === "tunnel" && !relay)} onClick={create} data-testid="create">
              {t("addroute.create")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function Choice({ active, onClick, label, testid }: { active: boolean; onClick: () => void; label: string; testid: string }) {
  return (
    <button data-testid={testid} onClick={onClick}
      className={`flex-1 rounded-md border px-3 py-2 text-sm ${active ? "border-brand bg-brand/10" : "border-gray-300 dark:border-gray-700"}`}>
      {label}
    </button>
  );
}
