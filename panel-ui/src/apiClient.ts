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

// Key under which impersonation tabs persist their access token. sessionStorage
// is per-tab, survives reload, and dies when the tab closes — exactly the
// lifetime we want for an impersonation session. We only populate this when
// the `no_refresh` flag is also set (i.e. this tab is an impersonation tab
// with no refresh cookie to recover from).
const IMP_TOKEN_KEY = "imp_access_token";

export function setAccessToken(token: string | null): void {
  accessToken = token;
  // Mirror to sessionStorage only for impersonation tabs so reload can recover
  // without hitting /auth/refresh (which has no cookie for these tabs).
  if (sessionStorage.getItem("no_refresh") === "1") {
    if (token) {
      sessionStorage.setItem(IMP_TOKEN_KEY, token);
    } else {
      sessionStorage.removeItem(IMP_TOKEN_KEY);
    }
  }
}

export function getAccessToken(): string | null {
  if (accessToken) return accessToken;
  // Impersonation tabs: rehydrate the in-memory token from sessionStorage
  // after a reload. Only trust sessionStorage when the no_refresh marker
  // is also present.
  if (sessionStorage.getItem("no_refresh") === "1") {
    const stored = sessionStorage.getItem(IMP_TOKEN_KEY);
    if (stored) {
      accessToken = stored;
      return stored;
    }
  }
  return null;
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

/**
 * Proactively refresh the access token using the HttpOnly refresh cookie.
 * Called by authProvider.check() on app mount to avoid spurious 401s.
 */
export async function refreshAccessToken(): Promise<string | null> {
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
      return Promise.reject(normalizeError(err));
    }
    // Don't try to refresh *during* auth calls; let them fail cleanly.
    const url = original.url ?? "";
    if (url.startsWith("/auth/")) {
      return Promise.reject(normalizeError(err));
    }
    // Don't refresh in impersonation tabs (marked by no_refresh flag in sessionStorage).
    // This allows the impersonation session to expire cleanly without attempting token rotation.
    if (sessionStorage.getItem("no_refresh") === "1") {
      return Promise.reject(normalizeError(err));
    }
    if (original._retry) {
      return Promise.reject(normalizeError(err));
    }
    original._retry = true;

    const tok = await refreshAccessToken();
    if (!tok) {
      return Promise.reject(normalizeError(err));
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

/**
 * Normalize axios errors by extracting the backend's structured error response.
 * Converts {"error":"domain_already_exists","detail":"..."} into a readable message.
 * Refine's notification provider will call err.message, so we set that field.
 */
function normalizeError(err: AxiosError): AxiosError {
  const status = err.response?.status;
  const data = err.response?.data as { error?: string; detail?: string } | undefined;
  const code = data?.error;
  const detail = data?.detail;

  // Prefer detail field if present, else humanize the error code, else fallback to original message.
  const message =
    detail ??
    (code ? humanizeErrorCode(code) : undefined) ??
    err.message ??
    `Request failed with status ${status ?? "unknown"}`;

  // Copy the error to preserve status, response, etc, but override the message.
  const wrapped = new Error(message) as AxiosError;
  wrapped.name = err.name;
  wrapped.config = err.config;
  wrapped.code = err.code;
  wrapped.request = err.request;
  wrapped.response = err.response;
  wrapped.isAxiosError = err.isAxiosError;
  wrapped.status = err.status;
  wrapped.toJSON = err.toJSON.bind(err);

  return wrapped;
}

/**
 * Human-friendly messages for common backend error codes.
 * Falls back to the code with underscores replaced by spaces if not found.
 */
function humanizeErrorCode(code: string): string {
  const messages: Record<string, string> = {
    domain_already_exists: "That domain is already taken",
    domain_quota_exceeded: "Your plan doesn't allow more domains",
    admin_cannot_host: "Admins can't host domains — create a regular user first",
    os_user_exists: "A Linux user with that name already exists",
    admin_has_no_os_account: "This user is an admin and has no OS account",
    cannot_delete_self: "You can't delete your own account",
    cannot_delete_last_admin: "Can't delete the last admin",
    unauthenticated: "Please log in again",
    invalid_token: "Session expired — please log in again",
    validation_failed: "Some fields are invalid",
    internal: "Something went wrong on the server",
  };
  return messages[code] ?? code.replace(/_/g, " ");
}

/**
 * Initiate phpMyAdmin SSO by issuing a redirect token for the given database.
 * Returns the URL to navigate to in the same tab.
 * The URL contains a live credential token and must not be logged.
 */
export async function ssoPhpMyAdmin(
  databaseId: string,
): Promise<{ redirect_url: string }> {
  const resp = await apiClient.post<{ redirect_url: string }>(
    "/sso/phpmyadmin",
    { database_id: databaseId },
  );
  return resp.data;
}

export async function ssoFileBrowser(): Promise<{ redirect_url: string }> {
  const resp = await apiClient.post<{ redirect_url: string }>(
    "/sso/filebrowser",
    {},
  );
  return resp.data;
}
// === PHP Settings API ===

export interface DomainPHPSettings {
  php_pool_id?: string | null;
  php_version?: string | null;
  php_memory_limit?: string | null;
  php_upload_max_filesize?: string | null;
  php_post_max_size?: string | null;
  php_max_input_vars?: number | null;
  php_max_execution_time?: number | null;
  php_max_input_time?: number | null;
}

export interface UpdateDomainPHPSettingsRequest {
  php_memory_limit?: string | null;
  php_upload_max_filesize?: string | null;
  php_post_max_size?: string | null;
  php_max_input_vars?: number | null;
  php_max_execution_time?: number | null;
  php_max_input_time?: number | null;
}

/**
 * Fetch PHP settings for a specific domain
 */
export async function getDomainPHPSettings(
  domainId: string,
): Promise<DomainPHPSettings> {
  const resp = await apiClient.get<DomainPHPSettings>(
    `/domains/${domainId}/php-settings`,
  );
  return resp.data;
}

/**
 * Update PHP settings for a specific domain
 */
export async function updateDomainPHPSettings(
  domainId: string,
  settings: UpdateDomainPHPSettingsRequest,
): Promise<void> {
  await apiClient.patch(`/domains/${domainId}/php-settings`, settings);
}
