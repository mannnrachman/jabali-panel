// apiClient.ts — the one axios instance the whole SPA uses.
//
// Two jobs:
//   1. Attach the in-memory access token (when present) as Authorization.
//   2. On a 401 for *any* non-auth route, try one silent refresh via the
//      /api/v1/auth/refresh endpoint; if that returns a fresh access
//      token, retry the original request. If refresh fails we give up
//      and let the caller see the 401 so the auth provider can route to
//      /login.
//
// Access tokens live in memory only — never in localStorage, because an
// XSS in the SPA would then be able to exfiltrate them. Refresh cookie
// is HttpOnly so JS cannot read it even if the SPA is compromised.

import axios, {
  type AxiosError,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from "axios";

const API_BASE = "/api/v1";

let accessToken: string | null = null;

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

export const apiClient = axios.create({
  baseURL: API_BASE,
  withCredentials: true, // send the refresh cookie
});

apiClient.interceptors.request.use((cfg: InternalAxiosRequestConfig) => {
  if (accessToken) {
    cfg.headers.set("Authorization", `Bearer ${accessToken}`);
  }
  return cfg;
});

// Track the in-flight refresh so a burst of 401s coalesces into one refresh.
let refreshPromise: Promise<string | null> | null = null;

async function refreshAccessToken(): Promise<string | null> {
  if (!refreshPromise) {
    refreshPromise = (async () => {
      try {
        const resp = await axios.post<{ access_token: string }>(
          `${API_BASE}/auth/refresh`,
          null,
          { withCredentials: true },
        );
        const tok = resp.data?.access_token ?? null;
        setAccessToken(tok);
        return tok;
      } catch {
        setAccessToken(null);
        return null;
      } finally {
        // Clear so the next 401 (later) can start a fresh refresh.
        refreshPromise = null;
      }
    })();
  }
  return refreshPromise;
}

type RetryConfig = AxiosRequestConfig & { _retry?: boolean };

apiClient.interceptors.response.use(
  (resp) => resp,
  async (err: AxiosError) => {
    const original = err.config as RetryConfig | undefined;
    if (!original || err.response?.status !== 401) {
      return Promise.reject(err);
    }
    // Don't try to refresh *during* auth calls; let them fail cleanly.
    const url = original.url ?? "";
    if (url.startsWith("/auth/")) {
      return Promise.reject(err);
    }
    if (original._retry) {
      return Promise.reject(err);
    }
    original._retry = true;

    const tok = await refreshAccessToken();
    if (!tok) {
      return Promise.reject(err);
    }
    original.headers = original.headers ?? {};
    // Headers may be an AxiosHeaders instance or a plain object; set both ways.
    if (typeof (original.headers as { set?: unknown }).set === "function") {
      (original.headers as { set: (k: string, v: string) => void }).set(
        "Authorization",
        `Bearer ${tok}`,
      );
    } else {
      (original.headers as Record<string, string>)["Authorization"] = `Bearer ${tok}`;
    }
    return apiClient(original);
  },
);
