import { useState } from "react";

export function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${u[i]}`;
}

export function fmtMoney(cents: number, currency = "USD"): string {
  if (cents === 0) return "Free";
  return new Intl.NumberFormat("en-US", { style: "currency", currency }).format(cents / 100);
}

export function fmtDate(s?: string): string {
  if (!s) return "—";
  const d = new Date(s);
  return d.toLocaleString();
}

export function Copy({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="btn-ghost"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        } catch { /* clipboard unavailable */ }
      }}
    >
      {done ? "Copied ✓" : label}
    </button>
  );
}

export function Bar({ value, max }: { value: number; max: number }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const danger = pct >= 90;
  return (
    <div className="h-2 w-full rounded-full bg-gray-200 dark:bg-gray-800">
      <div
        className={`h-2 rounded-full ${danger ? "bg-red-500" : "bg-brand"}`}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
