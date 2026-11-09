import { useEffect, useState } from "react";
import { api, FieldError } from "../api/client";
import { useT } from "../i18n";

type ValidateResp = { valid: boolean; fields?: FieldError[]; affectedRoutes?: string[] };

// ApplyDialog validates a proposed snapshot, shows affected routes / field
// errors, and runs onConfirm only when valid.
export function ApplyDialog({ snapshot, onConfirm, onClose }: {
  snapshot: unknown; onConfirm: () => Promise<void>; onClose: () => void;
}) {
  const t = useT();
  const [res, setRes] = useState<ValidateResp | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.post<ValidateResp>("/config/validate", snapshot).then(setRes).catch(() => setRes({ valid: false }));
  }, [snapshot]);
  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50">
      <div className="card w-96 space-y-3">
        <h2 className="font-semibold">{t("apply.title")}</h2>
        {!res && <p className="text-sm">{t("common.loading")}</p>}
        {res && !res.valid && (
          <div className="text-sm text-red-600">
            <p className="font-medium">{t("apply.invalid")}</p>
            <ul className="list-disc ml-4">{(res.fields ?? []).map((f, i) => <li key={i}>{f.path}: {f.message}</li>)}</ul>
          </div>
        )}
        {res?.valid && (
          <div className="text-sm">
            <p>{t("apply.affected")}: {res.affectedRoutes?.length ? res.affectedRoutes.join(", ") : t("common.none")}</p>
          </div>
        )}
        <div className="flex justify-end gap-2">
          <button className="btn-ghost" onClick={onClose}>{t("common.cancel")}</button>
          <button className="btn" disabled={!res?.valid || busy}
            onClick={async () => { setBusy(true); try { await onConfirm(); onClose(); } finally { setBusy(false); } }}>
            {t("apply.confirm")}
          </button>
        </div>
      </div>
    </div>
  );
}
