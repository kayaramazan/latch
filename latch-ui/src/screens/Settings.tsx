import { useEffect, useState } from "react";
import { api } from "../api/client";
import { useT, useLang } from "../i18n";

type Settings = {
  listen: string; language: string; trustedProxies: string[];
  events: { retentionDays: number; maxSizeMB: number };
  auth: { mode: string };
};

export function Settings() {
  const t = useT();
  const { setLang } = useLang();
  const [s, setS] = useState<Settings | null>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => { api.get<Settings>("/settings").then(setS); }, []);
  if (!s) return <p className="text-sm">{t("common.loading")}</p>;
  async function save() {
    const updated = await api.put<Settings>("/settings", s);
    setS(updated); setSaved(true); setTimeout(() => setSaved(false), 1500);
    if (updated.language === "en" || updated.language === "tr") setLang(updated.language);
  }
  return (
    <div className="space-y-3 max-w-lg">
      <h1 className="text-xl font-semibold">{t("settings.title")}</h1>
      <div className="card space-y-3">
        <div><label className="label">{t("settings.listen")}</label>
          <input className="input" value={s.listen} onChange={(e) => setS({ ...s, listen: e.target.value })} /></div>
        <div><label className="label">{t("settings.language")}</label>
          <select className="input" value={s.language} onChange={(e) => setS({ ...s, language: e.target.value })}>
            <option value="en">English</option><option value="tr">Türkçe</option>
          </select></div>
        <div><label className="label">{t("settings.retention")}</label>
          <input className="input" type="number" value={s.events.retentionDays}
            onChange={(e) => setS({ ...s, events: { ...s.events, retentionDays: Number(e.target.value) } })} /></div>
        <button className="btn" onClick={save}>{saved ? t("common.copied") : t("common.save")}</button>
      </div>
    </div>
  );
}
