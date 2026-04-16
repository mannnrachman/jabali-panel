// identity.ts — single source of truth for "who am I", shared across
// authProvider, RoleGate, and any page component that needs to know the
// caller's role.
//
// We memoize GET /me so a page change doesn't cost a round-trip. Logout
// clears the cache so the next sign-in starts clean.
import { apiClient } from "./apiClient";

export type Identity = {
  id: string;
  email: string;
  isAdmin: boolean;
  impersonatedBy: string | null;
};

let cached: Identity | null = null;
let inflight: Promise<Identity | null> | null = null;

/**
 * Fetch the current user's identity, memoized across the session.
 *
 * Returns null if we're not logged in (the /me call bubbles a 401, which
 * apiClient's refresh interceptor handles; if that also fails we land
 * here with a thrown error, which we catch and coerce to null so the
 * caller can branch on "logged out" cleanly).
 */
export async function getIdentity(): Promise<Identity | null> {
  if (cached) return cached;
  if (inflight) return inflight;

  inflight = (async () => {
    try {
      const resp = await apiClient.get<{
        id: string;
        email: string;
        is_admin: boolean;
        impersonated_by?: string | null;
      }>("/me");
      cached = {
        id: resp.data.id,
        email: resp.data.email,
        isAdmin: resp.data.is_admin,
        impersonatedBy: resp.data.impersonated_by ?? null,
      };
      return cached;
    } catch {
      return null;
    } finally {
      inflight = null;
    }
  })();

  return inflight;
}

/** Drop the cache. Call on logout or on any 401 that bubbles past refresh. */
export function clearIdentity(): void {
  cached = null;
  inflight = null;
}
