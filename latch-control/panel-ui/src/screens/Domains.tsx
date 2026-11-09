import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, CustomDomain, VerifyDomainResult } from "../api";
import { fmtDate } from "../lib";

export function Domains({ plan }: { plan: string }) {
  const qc = useQueryClient();
  const domains = useQuery({ queryKey: ["domains"], queryFn: () => api.get<CustomDomain[]>("/domains") });
  const [domain, setDomain] = useState("");
  const [err, setErr] = useState("");
  const [verifyMsg, setVerifyMsg] = useState<Record<string, string>>({});

  const add = useMutation({
    mutationFn: () => api.post<CustomDomain>("/domains", { domain }),
    onSuccess: () => { setDomain(""); setErr(""); qc.invalidateQueries({ queryKey: ["domains"] }); },
    onError: (e) => setErr(e instanceof ApiError ? e.message : "Could not add domain"),
  });
  const verify = useMutation({
    mutationFn: (id: string) => api.post<VerifyDomainResult>(`/domains/${id}/verify`, {}),
    onSuccess: (r, id) => {
      setVerifyMsg((m) => ({ ...m, [id]: r.verified ? "Verified ✓" : (r.detail || "Not verified yet") }));
      qc.invalidateQueries({ queryKey: ["domains"] });
    },
  });
  const del = useMutation({
    mutationFn: (id: string) => api.del(`/domains/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["domains"] }),
  });

  if (plan === "free") {
    return (
      <div className="card text-sm text-gray-500">
        Custom domains are available on paid plans. Visit <Link to="/billing" className="text-brand">Billing</Link> to
        upgrade, then point your own domain (e.g. <code>app.yourbrand.com</code>) at a tunnel.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="card space-y-2">
        <h2 className="font-medium">Add a custom domain</h2>
        <div className="flex items-end gap-2">
          <div className="flex-1">
            <label className="label">Domain</label>
            <input className="input" value={domain} placeholder="app.yourbrand.com" onChange={(e) => setDomain(e.target.value)} />
          </div>
          <button className="btn" disabled={!domain || add.isPending} onClick={() => add.mutate()}>Add</button>
        </div>
        {err && <div className="text-sm text-red-600">{err}</div>}
        <p className="text-xs text-gray-500">
          After adding, point your domain at the relay with a CNAME, then click <b>Verify</b>. The certificate is
          issued automatically once DNS resolves.
        </p>
      </div>

      <div className="card">
        <h2 className="mb-2 font-medium">Your domains</h2>
        {(domains.data ?? []).length === 0 && <div className="text-sm text-gray-500">No custom domains yet.</div>}
        <div className="divide-y divide-gray-100 dark:divide-gray-800">
          {(domains.data ?? []).map((d) => (
            <div key={d.id} className="flex items-center justify-between gap-2 py-2">
              <div className="min-w-0">
                <div className="font-medium">{d.domain}</div>
                <div className="text-xs text-gray-400">
                  added {fmtDate(d.createdAt)}
                  {verifyMsg[d.id] ? ` · ${verifyMsg[d.id]}` : ""}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <span className={`pill ${d.status === "verified" ? "bg-green-100 text-green-700" : "bg-yellow-100 text-yellow-700"}`}>
                  {d.status}
                </span>
                {d.status !== "verified" && (
                  <button className="btn-ghost" disabled={verify.isPending} onClick={() => verify.mutate(d.id)}>Verify</button>
                )}
                <button className="btn-danger" disabled={del.isPending} onClick={() => del.mutate(d.id)}>Delete</button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
