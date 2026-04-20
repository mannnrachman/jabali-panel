// authProvider.ts — Refine auth provider bound to Ory Kratos browser flows.
//
// M20: all authentication state is the `ory_kratos_session` cookie, which the
// browser attaches automatically to same-origin requests. There is no access
// token in JS, no refresh dance, no in-memory session copy. The SPA asks
// panel-api's /api/v1/me for the caller's identity — that endpoint already
// runs behind RequireKratosSession (which calls Kratos whoami under the
// hood) and returns the panel ULID + authoritative is_admin. Sourcing
// identity from /me instead of Kratos whoami directly means the ID we
// carry in JS matches what panel-api expects on /users/:id path params.
//
// Role-based routing: after login we fetch the identity and redirect to the
// appropriate shell — admins go to /jabali-admin, everyone else to
// /jabali-panel. Same logic on check() when we land on a bare / URL.
import type { AuthProvider } from "@refinedev/core";
import axios from "axios";

import { clearIdentity, getIdentity } from "./identity";
import { logoutBrowser } from "./kratos";

const ADMIN_HOME = "/jabali-admin";
const USER_HOME = "/jabali-panel";

export const authProvider: AuthProvider = {
  // login is handled in-component by pages/Login.tsx (which drives the
  // Kratos self-service flow directly) — the Login page navigates by role
  // after success. Refine still calls authProvider.login() in some code
  // paths, so we expose a stub that asks the user to use the /login page.
  login: async () => ({
    success: false,
    redirectTo: "/login",
  }),

  logout: async () => {
    try {
      await logoutBrowser();
    } catch {
      // best-effort: cookie may already be stale or Kratos may be down,
      // neither of which should block the client-side cleanup.
    }
    clearIdentity();
    return { success: true, redirectTo: "/login" };
  },

  // check: called by Refine on route transitions. Hits /api/v1/me;
  // the Kratos session cookie is sent automatically. No refresh step needed.
  check: async () => {
    const me = await getIdentity();
    if (me) return { authenticated: true };
    return { authenticated: false, redirectTo: "/login", logout: true };
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

// Exported so pages/Login.tsx can land the user on the right shell after
// a successful Kratos submission — keeps the role-routing constants in one
// place.
export function homeForRole(isAdmin: boolean): string {
  return isAdmin ? ADMIN_HOME : USER_HOME;
}
