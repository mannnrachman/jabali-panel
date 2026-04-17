// Refine auth provider bound to our /api/v1/auth/* + /api/v1/me endpoints.
//
// The refresh token is a HttpOnly cookie set by the panel on successful
// /auth/login; we never touch it from JS. The access token lives only in
// memory (see apiClient.ts) so an XSS on the SPA can't steal it.
//
// Role-based routing: after login we fetch the identity and redirect to
// the appropriate shell — admins go to /jabali-admin, everyone else to
// /jabali-panel. Same logic on check() when we land on a bare / URL.
import type { AuthProvider } from "@refinedev/core";
import axios, { AxiosError } from "axios";

import { apiClient, getAccessToken, refreshAccessToken, setAccessToken } from "./apiClient";
import { clearIdentity, getIdentity } from "./identity";

type LoginPayload = { access_token: string };

const ADMIN_HOME = "/jabali-admin";
const USER_HOME = "/jabali-panel";

export const authProvider: AuthProvider = {
  login: async ({ email, password }) => {
    try {
      const resp = await apiClient.post<LoginPayload>("/auth/login", {
        email,
        password,
      });
      setAccessToken(resp.data.access_token);
      // Freshly signed in — any stale cached identity is wrong.
      clearIdentity();
      const me = await getIdentity();
      return {
        success: true,
        redirectTo: me?.isAdmin ? ADMIN_HOME : USER_HOME,
        successNotification: { message: "Welcome back" },
      };
    } catch (err) {
      const ax = err as AxiosError<{ error?: string }>;
      const code = ax.response?.data?.error ?? "login_failed";
      return {
        success: false,
        error: {
          name: "Login failed",
          message:
            code === "invalid_credentials"
              ? "Incorrect email or password."
              : code === "rate_limited"
                ? "Too many attempts — try again in a minute."
                : "Could not sign in. Please try again.",
        },
      };
    }
  },

  logout: async () => {
    try {
      await apiClient.post("/auth/logout");
    } catch {
      // best-effort; cookie/token may already be stale
    }
    setAccessToken(null);
    clearIdentity();
    return { success: true, redirectTo: "/login" };
  },

  // check: called by Refine on route transitions. Fast-path uses the
  // in-memory token; otherwise we try /me, which the axios refresh
  // interceptor will silently retry with a refreshed token.
  check: async () => {
    // Fast path: we already have an access token in memory (or rehydrated
    // from sessionStorage for impersonation tabs — see apiClient.getAccessToken).
    if (getAccessToken()) return { authenticated: true };

    // Impersonation tabs never have a refresh cookie — calling /auth/refresh
    // would 401 and spam the console. If there's no token and no_refresh is
    // set, the session is genuinely over; send to /login cleanly.
    if (sessionStorage.getItem("no_refresh") === "1") {
      return { authenticated: false, redirectTo: "/login", logout: true };
    }

    // Fresh page load — no in-memory token. Try /auth/refresh first so
    // the subsequent /me call doesn't visibly 401 in the browser console
    // during the silent recovery. Refresh uses the HttpOnly cookie; if
    // the cookie is absent or invalid we route straight to /login without
    // ever hitting /me.
    const tok = await refreshAccessToken();
    if (!tok) {
      return { authenticated: false, redirectTo: "/login", logout: true };
    }

    const me = await getIdentity();
    return me
      ? { authenticated: true }
      : { authenticated: false, redirectTo: "/login", logout: true };
  },

  // getIdentity: read-through the shared cache so Refine components
  // (ThemedLayoutV2 header, etc.) see the same object RoleGate uses.
  getIdentity: async () => {
    const me = await getIdentity();
    if (!me) return null;
    return {
      id: me.id,
      name: me.email,
      email: me.email,
      isAdmin: me.isAdmin,
    };
  },

  onError: async (error) => {
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      clearIdentity();
      return { logout: true, redirectTo: "/login" };
    }
    return { error };
  },
};
