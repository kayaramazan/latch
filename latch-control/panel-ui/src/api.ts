// Thin fetch wrapper for the latch-control customer API. It carries the session
// cookie and attaches the CSRF token (from GET /api/me) to mutating requests.

let csrfToken = "";
export function setCsrf(token: string) { csrfToken = token; }

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
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
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText);
  return data as T;
}

export const api = {
  get: <T>(p: string) => req<T>("GET", p),
  post: <T>(p: string, b?: unknown) => req<T>("POST", p, b),
  put: <T>(p: string, b?: unknown) => req<T>("PUT", p, b),
  del: <T>(p: string) => req<T>("DELETE", p),
};

// --- types (mirror latch-control/internal/store + plan + handler payloads) ---

export type Plan = {
  id: string; name: string; priceCents: number; currency: string;
  maxTokens: number; maxDomains: number; maxConcurrent: number;
  bandwidthGbMonth: number; requestsPerMin: number;
  hardCapBandwidth: boolean; overageCentsPerGb: number;
};
export type Account = {
  id: string; name: string; status: string; planId: string; region: string;
  createdAt: string; suspendedAt?: string; suspendedReason?: string; trialExpiresAt?: string;
};
export type User = {
  id: string; accountId: string; email: string; role: string;
  emailVerifiedAt?: string; totpEnabledAt?: string; createdAt: string; lastLoginAt?: string;
};
export type Me = { account: Account; user: User; csrfToken: string };

export type TunnelToken = {
  id: string; accountId: string; projectId: string; relayId: string;
  name: string; hostnames?: string[]; tcp?: boolean; status: string;
  createdAt: string; lastSeenAt?: string;
};
export type CreateTokenResult = { token: TunnelToken; connectionString: string; hostname: string };

export type CustomDomain = {
  id: string; accountId: string; domain: string; relayId: string;
  status: string; verifiedAt?: string; createdAt: string;
};
export type VerifyDomainResult = { domain: string; verified: boolean; detail?: string };

export type APIKey = {
  id: string; accountId: string; name: string; prefix: string;
  createdAt: string; lastUsedAt?: string; revokedAt?: string;
};
export type CreateKeyResult = { key: APIKey; secret: string };

export type Usage = {
  plan: Plan; bytesIn: number; bytesOut: number; bytesTotal: number;
  bandwidthLimit: number; requests: number;
  tokens: number; tokenLimit: number; domains: number; domainLimit: number;
};

export type MfaSetup = { secret: string; otpauthUrl: string };
export type AuditEntry = {
  id: string; action: string; actor: string; targetType: string; targetId: string;
  ip?: string; createdAt: string;
};
