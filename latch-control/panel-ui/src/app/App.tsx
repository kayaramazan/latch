import { useQuery } from "@tanstack/react-query";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, setCsrf, Me } from "../api";
import { Auth } from "../screens/Auth";
import { Overview } from "../screens/Overview";
import { Tunnels } from "../screens/Tunnels";
import { Domains } from "../screens/Domains";
import { UsageScreen } from "../screens/Usage";
import { Billing } from "../screens/Billing";
import { Account } from "../screens/Account";
import { Download } from "../screens/Download";

const NAV: [string, string][] = [
  ["/", "Overview"],
  ["/tunnels", "Tunnels"],
  ["/domains", "Domains"],
  ["/usage", "Usage"],
  ["/billing", "Billing"],
  ["/download", "Download agent"],
  ["/account", "Account"],
];

export function App() {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const m = await api.get<Me>("/me");
      setCsrf(m.csrfToken);
      return m;
    },
  });

  if (me.isLoading) {
    return <div className="p-8 text-sm text-gray-500">Loading…</div>;
  }
  if (me.isError || !me.data) {
    return <Auth onDone={() => me.refetch()} />;
  }

  const m = me.data;
  return (
    <div className="mx-auto max-w-5xl p-4">
      <header className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="text-lg font-semibold">Latch</span>
          <span className="pill bg-brand/10 text-brand">{m.account.planId}</span>
          {m.account.status !== "active" && (
            <span className="pill bg-red-100 text-red-700">{m.account.status}</span>
          )}
        </div>
        <div className="text-sm text-gray-500">{m.user.email}</div>
      </header>

      <nav className="mb-6 flex flex-wrap gap-1 border-b border-gray-200 pb-2 dark:border-gray-800">
        {NAV.map(([to, label]) => (
          <NavLink
            key={to}
            to={to}
            end={to === "/"}
            className={({ isActive }) =>
              `rounded-md px-3 py-1.5 text-sm ${isActive ? "bg-brand text-white" : "hover:bg-gray-100 dark:hover:bg-gray-800"}`
            }
          >
            {label}
          </NavLink>
        ))}
      </nav>

      <main>
        <Routes>
          <Route path="/" element={<Overview me={m} />} />
          <Route path="/tunnels" element={<Tunnels />} />
          <Route path="/domains" element={<Domains plan={m.account.planId} />} />
          <Route path="/usage" element={<UsageScreen />} />
          <Route path="/billing" element={<Billing me={m} />} />
          <Route path="/download" element={<Download />} />
          <Route path="/account" element={<Account me={m} onLogout={() => me.refetch()} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}
