import { useEffect, useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api } from "../api/client";
import { useT, useLang } from "../i18n";
import { StatusStrip } from "./StatusStrip";
import { Login } from "../screens/Login";
import { Dashboard } from "../screens/Dashboard";
import { RoutesScreen } from "../screens/Routes";
import { AddRoute } from "../screens/AddRoute";
import { RouteDetail } from "../screens/RouteDetail";
import { Policies } from "../screens/Policies";
import { RateLimits } from "../screens/RateLimits";
import { Relays } from "../screens/Relays";
import { Live } from "../screens/Live";
import { History } from "../screens/History";
import { Settings } from "../screens/Settings";
import { Explain } from "../screens/Explain";

const nav = [
  ["/", "nav.dashboard"],
  ["/routes", "nav.routes"],
  ["/policies", "nav.policies"],
  ["/ratelimits", "nav.ratelimits"],
  ["/relays", "nav.relays"],
  ["/live", "nav.live"],
  ["/history", "nav.history"],
  ["/explain", "nav.explain"],
  ["/settings", "nav.settings"],
] as const;

export function App() {
  const t = useT();
  const { lang, setLang } = useLang();
  const [authed, setAuthed] = useState<boolean | null>(null);

  async function check() {
    try {
      const me = await api.get<{ authenticated: boolean; authRequired: boolean }>("/auth/me");
      setAuthed(me.authenticated);
    } catch {
      setAuthed(false);
    }
  }
  useEffect(() => { check(); }, []);

  if (authed === null) return <div className="p-8 text-sm">{t("common.loading")}</div>;
  if (!authed) return <Login onDone={check} />;

  return (
    <div className="min-h-screen flex">
      <aside className="w-52 shrink-0 border-r border-gray-200 dark:border-gray-800 p-3 flex flex-col">
        <div className="font-semibold text-lg px-2 py-2">{t("app.name")}</div>
        <nav className="flex-1 space-y-0.5">
          {nav.map(([to, key]) => (
            <NavLink key={to} to={to} end={to === "/"}
              className={({ isActive }) =>
                `block rounded-md px-3 py-1.5 text-sm ${isActive ? "bg-brand text-white" : "hover:bg-gray-100 dark:hover:bg-gray-800"}`}>
              {t(key)}
            </NavLink>
          ))}
        </nav>
        <select className="input mt-2" value={lang} onChange={(e) => setLang(e.target.value as "en" | "tr")}>
          <option value="en">English</option>
          <option value="tr">Türkçe</option>
        </select>
      </aside>
      <main className="flex-1 min-w-0">
        <StatusStrip />
        <div className="p-6 max-w-5xl">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/routes" element={<RoutesScreen />} />
            <Route path="/routes/new" element={<AddRoute />} />
            <Route path="/routes/:id" element={<RouteDetail />} />
            <Route path="/policies" element={<Policies />} />
            <Route path="/ratelimits" element={<RateLimits />} />
            <Route path="/relays" element={<Relays />} />
            <Route path="/live" element={<Live />} />
            <Route path="/history" element={<History />} />
            <Route path="/explain" element={<Explain />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </div>
      </main>
    </div>
  );
}
