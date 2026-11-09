// Thin fetch wrapper for the latchd management API. It attaches the CSRF token
// to mutating requests and surfaces structured validation errors.

let csrfToken = "";
export function setCsrf(token: string) { csrfToken = token; }

export type FieldError = { path: string; code: string; message: string };
export class ApiError extends Error {
  status: number;
  fields?: FieldError[];
  constructor(status: number, message: string, fields?: FieldError[]) {
    super(message);
    this.status = status;
    this.fields = fields;
  }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const mutating = method !== "GET" && method !== "HEAD";
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (mutating && csrfToken) headers["X-CSRF-Token"] = csrfToken;
  const res = await fetch(`/api${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    const msg = data?.error ?? res.statusText;
    throw new ApiError(res.status, msg, data?.fields);
  }
  return data as T;
}

export const api = {
  get: <T>(p: string) => req<T>("GET", p),
  post: <T>(p: string, b?: unknown) => req<T>("POST", p, b),
  put: <T>(p: string, b?: unknown) => req<T>("PUT", p, b),
  del: <T>(p: string) => req<T>("DELETE", p),
};

// Types mirror proto/model (only the fields the UI uses).
export type Route = {
  id: string; name: string; enabled: boolean; target: string;
  exposure: "local" | "tunnel"; hostnames: string[]; relayId?: string;
  kind: "http" | "tcp"; pathPrefix: string; tls: string;
  ipPolicyId?: string; rateLimitIds: string[];
};
export type Relay = { id: string; name: string; host: string; port: number; baseDomain?: string };
export type RelayStatus = {
  relayId: string; relayName: string; addr: string; state: string;
  rttMs: number; routes: number; syncState: string; publicUrls?: string[]; lastError?: string;
};
export type IPPolicy = {
  id: string; name: string; defaultAction: "allow" | "deny";
  rules: { action: "allow" | "deny"; cidr?: string; country?: string }[];
  onDeny: { mode: string; status?: number }; global: boolean;
};
export type RateLimitRule = {
  id: string; name: string; keys: string[]; limit: number; windowSec: number;
  onExceed: { mode: string; maxDelayMs?: number }; global: boolean;
};
export type AccessEvent = {
  ts: string; source: string; relayId?: string; routeId?: string; hostname: string;
  clientIp: string; country?: string; method?: string; path?: string; status: number;
  durationMs: number; decision: string; denyReason?: string; ruleId?: string; where: string;
};
export type Dashboard = {
  windowSec: number; requests: number; requestsPerSec: number; denied: number;
  p50Ms: number; p95Ms: number; p99Ms: number; statusClasses: Record<string, number>;
  topRoutes: { routeId: string; hostname: string; count: number; denied: number }[];
  currentlyBlocked: AccessEvent[];
  topBlockedIpsLastHour: { clientIp: string; count: number }[];
};
export type Explanation = {
  routeId: string; where: string; clientIp: string; country?: string;
  result: string; denyReason?: string; ruleId?: string;
  steps: { stage: string; detail: string; matched: boolean; result?: string }[];
};
export type LocalPort = { port: number; address: string; process?: string; pid?: number };
export type Status = {
  version: string; configVersion: number; relays: RelayStatus[]; health?: Record<string, string>;
};
