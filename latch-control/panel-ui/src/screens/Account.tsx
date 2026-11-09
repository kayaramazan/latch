import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, APIKey, CreateKeyResult, Me, MfaSetup } from "../api";
import { Copy, fmtDate } from "../lib";

export function Account({ me, onLogout }: { me: Me; onLogout: () => void }) {
  return (
    <div className="space-y-4">
      <Mfa me={me} />
      <Keys />
      <DataAndDanger me={me} onLogout={onLogout} />
    </div>
  );
}

function Mfa({ me }: { me: Me }) {
  const qc = useQueryClient();
  const [setup, setSetup] = useState<MfaSetup | null>(null);
  const [code, setCode] = useState("");
  const [err, setErr] = useState("");
  const enabled = !!me.user.totpEnabledAt;

  const begin = useMutation({
    mutationFn: () => api.post<MfaSetup>("/mfa/setup", {}),
    onSuccess: (r) => setSetup(r),
  });
  const enable = useMutation({
    mutationFn: () => api.post("/mfa/enable", { code }),
    onSuccess: () => { setSetup(null); setCode(""); setErr(""); qc.invalidateQueries({ queryKey: ["me"] }); },
    onError: (e) => setErr(e instanceof ApiError ? e.message : "Invalid code"),
  });

  return (
    <div className="card space-y-2">
      <div className="flex items-center justify-between">
        <h2 className="font-medium">Two-factor authentication</h2>
        <span className={`pill ${enabled ? "bg-green-100 text-green-700" : "bg-gray-100 text-gray-600"}`}>
          {enabled ? "enabled" : "off"}
        </span>
      </div>
      {enabled && <p className="text-sm text-gray-500">Your account is protected with an authenticator app.</p>}
      {!enabled && !setup && (
        <button className="btn" disabled={begin.isPending} onClick={() => begin.mutate()}>Enable 2FA</button>
      )}
      {!enabled && setup && (
        <div className="space-y-2">
          <p className="text-sm text-gray-500">Add this to your authenticator app, then enter the 6-digit code.</p>
          <div className="flex items-center gap-2">
            <div className="code flex-1 rounded bg-gray-100 p-2 dark:bg-gray-800">{setup.otpauthUrl}</div>
            <Copy text={setup.otpauthUrl} />
          </div>
          <div className="text-xs text-gray-500">Secret: <span className="code">{setup.secret}</span></div>
          <div className="flex items-end gap-2">
            <div>
              <label className="label">Code</label>
              <input className="input" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" inputMode="numeric" />
            </div>
            <button className="btn" disabled={!code || enable.isPending} onClick={() => enable.mutate()}>Confirm</button>
          </div>
          {err && <div className="text-sm text-red-600">{err}</div>}
        </div>
      )}
    </div>
  );
}

function Keys() {
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["apikeys"], queryFn: () => api.get<APIKey[]>("/apikeys") });
  const [name, setName] = useState("");
  const [secret, setSecret] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => api.post<CreateKeyResult>("/apikeys", { name }),
    onSuccess: (r) => { setSecret(r.secret); setName(""); qc.invalidateQueries({ queryKey: ["apikeys"] }); },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api.del(`/apikeys/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["apikeys"] }),
  });
  const active = (keys.data ?? []).filter((k) => !k.revokedAt);

  return (
    <div className="card space-y-2">
      <h2 className="font-medium">API keys</h2>
      <p className="text-sm text-gray-500">For automating tunnels from CI or scripts.</p>
      <div className="flex items-end gap-2">
        <div className="flex-1">
          <label className="label">Name</label>
          <input className="input" value={name} placeholder="ci-pipeline" onChange={(e) => setName(e.target.value)} />
        </div>
        <button className="btn" disabled={!name || create.isPending} onClick={() => create.mutate()}>Create</button>
      </div>
      {secret && (
        <div className="rounded-md border border-brand p-2">
          <div className="label mb-0 text-brand">New key (shown once)</div>
          <div className="flex items-center gap-2">
            <div className="code flex-1">{secret}</div>
            <Copy text={secret} />
          </div>
        </div>
      )}
      <div className="divide-y divide-gray-100 dark:divide-gray-800">
        {active.map((k) => (
          <div key={k.id} className="flex items-center justify-between py-2 text-sm">
            <div>
              <span className="font-medium">{k.name}</span>{" "}
              <span className="code text-gray-500">{k.prefix}…</span>
              <div className="text-xs text-gray-400">created {fmtDate(k.createdAt)}</div>
            </div>
            <button className="btn-danger" disabled={revoke.isPending} onClick={() => revoke.mutate(k.id)}>Revoke</button>
          </div>
        ))}
        {active.length === 0 && <div className="text-sm text-gray-500">No API keys.</div>}
      </div>
    </div>
  );
}

function DataAndDanger({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const [confirm, setConfirm] = useState("");

  async function exportData() {
    try {
      const res = await fetch("/api/account/export", { credentials: "same-origin" });
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `latch-account-${me.account.id}.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch { alert("Export failed"); }
  }

  const del = useMutation({
    mutationFn: () => api.del("/account"),
    onSuccess: () => onLogout(),
    onError: (e) => alert(e instanceof ApiError ? e.message : "Delete failed"),
  });
  const logout = useMutation({
    mutationFn: () => api.post("/logout", {}),
    onSuccess: () => onLogout(),
  });

  return (
    <div className="card space-y-3">
      <h2 className="font-medium">Your data</h2>
      <div className="flex flex-wrap gap-2">
        <button className="btn-ghost" onClick={exportData}>Export account data</button>
        <button className="btn-ghost" disabled={logout.isPending} onClick={() => logout.mutate()}>Sign out</button>
      </div>

      <div className="mt-2 rounded-md border border-red-200 p-3 dark:border-red-900">
        <div className="font-medium text-red-600">Delete account</div>
        <p className="text-sm text-gray-500">
          Permanently deletes your account, tunnels and data. Type <b>DELETE</b> to confirm.
        </p>
        <div className="mt-2 flex items-end gap-2">
          <input className="input max-w-40" value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder="DELETE" />
          <button className="btn-danger" disabled={confirm !== "DELETE" || del.isPending} onClick={() => del.mutate()}>
            Delete permanently
          </button>
        </div>
      </div>
    </div>
  );
}
