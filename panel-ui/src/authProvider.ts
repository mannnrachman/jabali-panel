// Refine auth provider bound to our /api/v1/auth/* + /api/v1/me endpoints.
//
// The refresh token is a HttpOnly cookie set by the panel on successful
// /auth/login; we never touch it from JS. The access token lives only in
// memory (see apiClient.ts) so an XSS on the SPA can't steal it.
import type { AuthProvider } from "@refinedev/core";
import axios, { AxiosError } from "axios";

import { apiClient, getAccessToken, setAccessToken } from "./apiClient";

type LoginPayload = { access_token: string };

export const authProvider: AuthProvider = {
  login: async ({ email, password }) => {
    try {
      const resp = await apiClient.post<LoginPayload>("/auth/login", {
        email,
        password,
      });
      setAccessToken(resp.data.access_token);
      return {
        success: true,
        redirectTo: "/",
        successNotification: { message: "Welcome back" },
      };
    } catch (err) {
      // Map the panel's typed error codes to user-facing messages without
      // leaking backend wording to the user verbatim.
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
    return { success: true, redirectTo: "/login" };
  },

  // check: Refine calls this on route change. With no in-memory token, we
  // try a silent refresh (axios interceptor will do it on the first 401)
  // by calling /me. If that 401s after refresh, we're truly logged out.
  check: async () => {
    // Fast path — we still have a token in memory, trust it optimistically.
    if (getAccessToken()) {
      return { authenticated: true };
    }
    // Otherwise attempt a /me call; the interceptor refreshes under the
    // hood. If we get here without a token AFTER refresh, bail.
    try {
      await apiClient.get("/me");
      return { authenticated: true };
    } catch {
      return {
        authenticated: false,
        redirectTo: "/login",
        logout: true,
      };
    }
  },

  // getIdentity: populates the <ThemedLayout> header with who's logged in.
  getIdentity: async () => {
    try {
      const resp = await apiClient.get<{
        id: string;
        email: string;
        is_admin: boolean;
      }>("/me");
      return {
        id: resp.data.id,
        name: resp.data.email,
        email: resp.data.email,
        isAdmin: resp.data.is_admin,
      };
    } catch {
      return null;
    }
  },

  onError: async (error) => {
    // Any 401 that bubbles out (i.e. refresh already tried and failed) →
    // force logout. Other errors fall through unchanged.
    if (axios.isAxiosError(error) && error.response?.status === 401) {
      return { logout: true, redirectTo: "/login" };
    }
    return { error };
  },
};
