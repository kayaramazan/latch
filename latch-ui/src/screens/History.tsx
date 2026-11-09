import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useMutate, useInvalidate } from "../api/hooks";
import { useT } from "../i18n";
import { useRef } from "react";

type Version = { version: number; createdAt: string; author: string; summary: string };

export function History() {
  const t = useT();
  const inv = useInvalidate();
  const { data: versions } = useQuery({ queryKey: ["versions"], queryFn: () => api.get<Version[]>("/config/versions?limit=50") });
  const rollback = useMutate((n: number) => api.post(`/config/rollback/${n}`), ["versions", "routes", "relays", "status"]);
  const fileRef = useRef<HTMLInputElement>(null);

  async function doExport() {
    const data = await api.get<unknown>("/backup");
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob); a.download = "latch-backup.json"; a.click();
  }
  async function doImport(file: File) {
    const text = await file.text();
    await api.post("/backup?mode=replace", JSON.parse(text));
    inv(["versions", "routes", "relays", "status"]);
  }

  return (
    <div className="space-y-4 max-w-2xl">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t("history.title")}</h1>
        <div className="flex gap-2">
          <button className="btn-ghost" onClick={doExport}>{t("history.export")}</button>
          <button className="btn-ghost" onClick={() => fileRef.current?.click()}>{t("history.import")}</button>
          <input ref={fileRef} type="file" accept="application/json" className="hidden"
            onChange={(e) => e.target.files?.[0] && doImport(e.target.files[0])} />
        </div>
      </div>
      <div className="card">
        <table className="w-full text-sm">
          <thead className="text-gray-500 text-left"><tr><th className="py-1">v</th><th>author</th><th>summary</th><th></th></tr></thead>
          <tbody>
            {versions?.map((v) => (
              <tr key={v.version} className="border-t border-gray-100 dark:border-gray-800">
                <td className="py-1">{v.version}</td><td>{v.author}</td><td>{v.summary}</td>
                <td className="text-right"><button className="btn-ghost text-xs" onClick={() => rollback.mutate(v.version)}>{t("history.rollback")}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
