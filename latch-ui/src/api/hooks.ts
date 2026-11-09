import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, Route, Relay, IPPolicy, RateLimitRule, Status, Dashboard, AccessEvent, LocalPort } from "./client";

export const useRoutes = () => useQuery({ queryKey: ["routes"], queryFn: () => api.get<Route[]>("/routes") });
export const useRelays = () => useQuery({ queryKey: ["relays"], queryFn: () => api.get<Relay[]>("/relays") });
export const useIPPolicies = () => useQuery({ queryKey: ["ip-policies"], queryFn: () => api.get<IPPolicy[]>("/ip-policies") });
export const useRateLimits = () => useQuery({ queryKey: ["rate-limits"], queryFn: () => api.get<RateLimitRule[]>("/rate-limits") });
export const useStatus = () => useQuery({ queryKey: ["status"], queryFn: () => api.get<Status>("/status"), refetchInterval: 4000 });
export const useDashboard = (window: number) =>
  useQuery({ queryKey: ["dashboard", window], queryFn: () => api.get<Dashboard>(`/dashboard?window=${window}`), refetchInterval: 5000 });
export const useLocalPorts = () =>
  useQuery({ queryKey: ["local-ports"], queryFn: () => api.get<LocalPort[]>("/local/ports"), staleTime: 5000 });
export const useEvents = (n = 100) =>
  useQuery({ queryKey: ["events", n], queryFn: () => api.get<AccessEvent[]>(`/events?n=${n}`) });

export function useInvalidate() {
  const qc = useQueryClient();
  return (keys: string[]) => keys.forEach((k) => qc.invalidateQueries({ queryKey: [k] }));
}

export function useMutate<TBody>(fn: (b: TBody) => Promise<unknown>, invalidate: string[]) {
  const inv = useInvalidate();
  return useMutation({ mutationFn: fn, onSuccess: () => inv(invalidate) });
}
