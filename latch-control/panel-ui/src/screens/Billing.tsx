import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiError, Me, Plan } from "../api";
import { fmtMoney } from "../lib";

export function Billing({ me }: { me: Me }) {
  const plans = useQuery({ queryKey: ["plans"], queryFn: () => api.get<Plan[]>("/plans") });
  const checkout = useMutation({
    mutationFn: (planId: string) => api.post<{ checkoutUrl: string }>("/billing/checkout", { planId }),
    onSuccess: (r) => { if (r.checkoutUrl) window.open(r.checkoutUrl, "_blank", "noopener"); },
    onError: (e) => alert(e instanceof ApiError ? e.message : "Checkout failed"),
  });
  const current = me.account.planId;

  return (
    <div className="space-y-4">
      <div className="card">
        <h2 className="font-medium">Current plan</h2>
        <div className="text-2xl font-semibold capitalize">{current}</div>
        <p className="text-sm text-gray-500">
          Billing runs through our payment provider (Merchant of Record) — taxes and invoices are handled for you.
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        {(plans.data ?? []).map((p) => {
          const active = p.id === current;
          return (
            <div key={p.id} className={`card space-y-3 ${active ? "border-brand" : ""}`}>
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold">{p.name}</h3>
                {active && <span className="pill bg-brand/10 text-brand">current</span>}
              </div>
              <div className="text-2xl font-semibold">
                {fmtMoney(p.priceCents, p.currency)}
                {p.priceCents > 0 && <span className="text-sm font-normal text-gray-500">/mo</span>}
              </div>
              <ul className="space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{p.maxTokens} tunnel{p.maxTokens > 1 ? "s" : ""}</li>
                <li>{p.bandwidthGbMonth} GB bandwidth / month</li>
                <li>{p.maxDomains > 0 ? `${p.maxDomains} custom domains` : "No custom domains"}</li>
                <li>{p.requestsPerMin} requests / min</li>
                <li>{p.hardCapBandwidth ? "Hard cap at limit" : `Overage ${(p.overageCentsPerGb / 100).toFixed(2)}/GB`}</li>
              </ul>
              {!active && p.priceCents >= 0 && (
                <button
                  className="btn w-full"
                  disabled={checkout.isPending || p.id === "free"}
                  onClick={() => checkout.mutate(p.id)}
                >
                  {p.id === "free" ? "—" : `Upgrade to ${p.name}`}
                </button>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
