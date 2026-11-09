import { useState } from "react";
import { api } from "../api/client";
import { useRelays, useStatus, useMutate } from "../api/hooks";
import { useT } from "../i18n";

export function Relays() {
  const t = useT();
  const { data: relays } = useRelays();
  const { data: status } = useStatus();
  const add = useMutate((b: unknown) => api.post("/relays", b), ["relays", "status"]);
  const del = useMutate((id: string) => api.del(`/relays/${id}`), ["relays", "status"]);
  const [conn, setConn] = useState("");
  const [wizard, setWizard] = useState(false);

  function addFromConn() {
    // Parse latch://host:port/?token=...&name=...
    try {
      const u = new URL(conn.replace(/^latch:/, "https:"));
      const token = u.searchParams.get("token") ?? "";
      const name = u.searchParams.get("name") || u.hostname;
      add.mutate({ name, host: u.hostname, port: Number(u.port) || 7443, token });
      setConn("");
    } catch { /* ignore */ }
  }
  const statusFor = (id: string) => status?.relays.find((r) => r.relayId === id);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("relays.title")}</h1>
        <button className="btn-ghost" onClick={() => setWizard((v) => !v)}>{t("relays.setup")}</button>
      </div>
      {wizard && <RelayWizard />}
      <div className="card space-y-2">
        <h2 className="font-medium">{t("relays.connect")}</h2>
        <input className="input font-mono text-xs" placeholder="latch://relay.example.com:7443/?token=lt_…" value={conn} onChange={(e) => setConn(e.target.value)} />
        <button className="btn" disabled={!conn} onClick={addFromConn}>{t("common.add")}</button>
      </div>
      <div className="grid md:grid-cols-2 gap-3">
        {relays?.map((r) => {
          const s = statusFor(r.id);
          return (
            <div key={r.id} className="card space-y-1 text-sm">
              <div className="flex items-center justify-between">
                <span className="font-medium">{r.name}</span>
                <button className="btn-ghost text-red-600" onClick={() => del.mutate(r.id)}>{t("common.delete")}</button>
              </div>
              <div className="text-gray-500">{r.host}:{r.port}</div>
              <div>{t("relays.state")}: <b>{s?.state ?? "—"}</b> · RTT {s?.rttMs?.toFixed(0) ?? "—"}ms</div>
              <div>{t("relays.sync")}: <SyncBadge sync={s?.syncState} /></div>
              {s?.publicUrls?.map((u) => <div key={u} className="font-mono text-xs truncate">{u}</div>)}
              {s?.lastError && <div className="text-xs text-red-600">{s.lastError}</div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function SyncBadge({ sync }: { sync?: string }) {
  const t = useT();
  if (sync === "out_of_sync") return <span className="pill bg-red-100 text-red-700">out_of_sync — {t("relays.resync")}</span>;
  return <span className="pill bg-green-100 text-green-700">{sync ?? "n/a"}</span>;
}

function RelayWizard() {
  const t = useT();
  const [host, setHost] = useState("");
  const [email, setEmail] = useState("");
  const [cmd, setCmd] = useState<{ install: string; wizard: string } | null>(null);
  return (
    <div className="card space-y-2">
      <h2 className="font-medium">{t("relaywizard.title")}</h2>
      <input className="input" placeholder={t("relaywizard.publicHost")} value={host} onChange={(e) => setHost(e.target.value)} />
      <input className="input" placeholder={t("relaywizard.email")} value={email} onChange={(e) => setEmail(e.target.value)} />
      <button className="btn" disabled={!host} onClick={async () => {
        setCmd(await api.post("/relays/setup-script", { publicHost: host, email }));
      }}>{t("relaywizard.generate")}</button>
      {cmd && (
        <div className="space-y-1">
          <pre className="bg-gray-100 dark:bg-gray-800 p-2 rounded text-xs overflow-x-auto">{cmd.install}</pre>
          <pre className="bg-gray-100 dark:bg-gray-800 p-2 rounded text-xs overflow-x-auto">{cmd.wizard}</pre>
        </div>
      )}
    </div>
  );
}
