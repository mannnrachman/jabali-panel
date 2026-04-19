// identity.ts — single source of truth for "who am I", shared across
// authProvider, RoleGate, and any page component that needs to know the
// caller's role.
//
// M20: identity comes from Kratos's /sessions/whoami (via /.ory/). The
// is_admin trait is authoritative; a missing trait defaults to false so
// compromising a user token can't accidentally elevate. impersonatedBy
// is always null post-M20 (M5a impersonation was dropped by step 6), but
// the field stays on the shape for one cycle so callers don't break.
import { whoami } from "./kratos";

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
 * Returns null when we're not logged in. Re-throws on transient upstream
 * errors (Kratos 5xx) so the caller can tell the difference between "no
 * session" and "identity service blip" — only the former should trigger
 * a /login redirect.
 */
export async function getIdentity(): Promise<Identity | null> {
  if (cached) return cached;
  if (inflight) return inflight;

  inflight = (async () => {
    try {
      const session = await whoami();
      if (!session?.identity) {
        return null;
      }
      const traits = session.identity.traits ?? { email: "" };
      cached = {
        id: session.identity.id,
        email: traits.email ?? "",
        isAdmin: traits.is_admin === true,
        impersonatedBy: null,
      };
      return cached;
    } catch {
      // Network / 5xx — treat as "unknown" rather than "logged out". The
      // caller can retry or surface a toast; we don't want a Kratos blip
      // to force everyone to re-login.
      return null;
    } finally {
      inflight = null;
    }
  })();

  return inflight;
}

/** Drop the cache. Call on logout or on any 401 that bubbles from whoami. */
export function clearIdentity(): void {
  cached = null;
  inflight = null;
}
