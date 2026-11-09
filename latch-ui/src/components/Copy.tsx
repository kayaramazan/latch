import { useState } from "react";
import { useT } from "../i18n";

export function Copy({ text }: { text: string }) {
  const t = useT();
  const [done, setDone] = useState(false);
  return (
    <button className="btn-ghost text-xs" onClick={async () => {
      try { await navigator.clipboard.writeText(text); setDone(true); setTimeout(() => setDone(false), 1200); } catch { /* ignore */ }
    }}>{done ? t("common.copied") : t("common.copy")}</button>
  );
}
