import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, CreateTokenResult, TunnelToken } from "../api";
import { Copy, fmtDate } from "../lib";

export function Tunnels() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: () => api.get<TunnelToken[]>("/tokens") });
  const [name, setName] = useState("");
  const [tcp, setTcp] = useState(false);
  const [created, setCreated] = useState<CreateTokenResult | null>(null);
  const [err, setErr] = useState("");

  const create = useMutation({
    mutationFn: () => api.post<CreateTokenResult>("/tokens", { name, tcp }),
    onSuccess: (r) => {
      setCreated(r);
      setName("");
      setErr("");
      qc.invalidateQueries({ queryKey: ["tokens"] });
      qc.invalidateQueries({ queryKey: ["usage"] });
    },
    onError: (e) => setErr(e instanceof ApiError ? e.message : "Could not create tunnel"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.del(`/tokens/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["tokens"] });
      qc.invalidateQueries({ queryKey: ["usage"] });
    },
  });

  const active = (tokens.data ?? []).filter((t) => t.status === "active");
  const revoked = (tokens.data ?? []).filter((t) => t.status !== "active");

  return (
    <div className="space-y-4">
      <div className="card space-y-3">
        <h2 className="font-medium">Create a tunnel</h2>
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex-1 min-w-48">
            <label className="label">Name</label>
            <input className="input" value={name} placeholder="my-app" onChange={(e) => setName(e.target.value)} />
          </div>
          <label className="flex items-center gap-2 pb-2 text-sm">
            <input type="checkbox" checked={tcp} onChange={(e) => setTcp(e.target.checked)} /> TCP tunnel
          </label>
          <button className="btn" disabled={!name || create.isPending} onClick={() => create.mutate()}>
            {create.isPending ? "…" : "Create"}
          </button>
        </div>
        {err && <div className="text-sm text-red-600">{err}</div>}
        <p className="text-xs text-gray-500">
          A tunnel gives you a public hostname on our relay. You run the lightweight agent on the machine
          whose port you want to expose, and traffic reaches it over an encrypted tunnel with valid HTTPS.
        </p>
      </div>

      {created && <ConnectCard result={created} onClose={() => setCreated(null)} />}

      <div className="card">
        <h2 className="mb-2 font-medium">Active tunnels</h2>
        {active.length === 0 && <div className="text-sm text-gray-500">No active tunnels yet.</div>}
        <div className="divide-y divide-gray-100 dark:divide-gray-800">
          {active.map((t) => (
            <div key={t.id} className="flex items-center justify-between gap-2 py-2">
              <div className="min-w-0">
                <div className="font-medium">{t.name}</div>
                <div className="code truncate text-gray-500">
                  {(t.hostnames ?? []).map((h) => `https://${h}`).join(", ")}
                </div>
                <div className="text-xs text-gray-400">
                  {t.tcp ? "TCP · " : ""}created {fmtDate(t.createdAt)}
                  {t.lastSeenAt ? ` · last seen ${fmtDate(t.lastSeenAt)}` : " · never connected"}
                </div>
              </div>
              <button className="btn-danger" disabled={revoke.isPending} onClick={() => revoke.mutate(t.id)}>
                Revoke
              </button>
            </div>
          ))}
        </div>
      </div>

      {revoked.length > 0 && (
        <div className="card">
          <h2 className="mb-2 font-medium text-gray-500">Revoked</h2>
          <div className="space-y-1 text-sm text-gray-400">
            {revoked.map((t) => (
              <div key={t.id}>{t.name} — {(t.hostnames ?? []).join(", ")}</div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function ConnectCard({ result, onClose }: { result: CreateTokenResult; onClose: () => void }) {
  const commands = [
    "# Install the agent (macOS/Linux):",
    "curl -sSL https://get.latch.dev/install.sh | sh",
    "",
    "# Connect this machine and expose a local port:",
    `latchd relay add --url '${result.connectionString}'`,
    `latchd route add --target 127.0.0.1:3000 --tunnel <relay-id> --hostname ${result.hostname}`,
  ].join("\n");

  return (
    <div className="card space-y-3 border-brand">
      <div className="flex items-center justify-between">
        <h2 className="font-medium text-brand">Tunnel ready — connect your machine</h2>
        <button className="btn-ghost" onClick={onClose}>Done</button>
      </div>
      <div>
        <div className="label">Your public URL</div>
        <div className="code">https://{result.hostname}</div>
      </div>
      <div>
        <div className="label">Connection string (shown once)</div>
        <div className="flex items-center gap-2">
          <div className="code flex-1 rounded bg-gray-100 p-2 dark:bg-gray-800">{result.connectionString}</div>
          <Copy text={result.connectionString} />
        </div>
      </div>
      <div>
        <div className="mb-1 flex items-center justify-between">
          <div className="label mb-0">Run on the machine you want to expose</div>
          <Copy text={commands} label="Copy commands" />
        </div>
        <pre className="block">{commands}</pre>
      </div>
      <p className="text-xs text-gray-500">
        Keep the connection string safe: it is shown only now. You can revoke this tunnel at any time.
      </p>
    </div>
  );
}
