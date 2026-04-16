// LandingRedirect — the "where should I go?" dispatcher for any URL
// that isn't pre-committed to a shell. Used by:
//   /                          — bare-root entry after login or bookmark
//   /login (while logged in)   — bounced forward instead of seeing the form again
//   * (catch-all)              — unknown URLs after auth
//
// Reads the identity once, redirects to the matching shell's home.
// During the identity fetch we render nothing (not even a spinner) —
// the network call is ≤100ms against a healthy backend, and a flash of
// spinner is worse UX than a blank frame.
import { useEffect, useState } from "react";
import { Navigate } from "react-router";

import { getIdentity, type Identity } from "../identity";

export function LandingRedirect() {
  const [state, setState] = useState<
    { status: "loading" } | { status: "resolved"; me: Identity | null }
  >({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    getIdentity().then((me) => {
      if (!cancelled) setState({ status: "resolved", me });
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (state.status === "loading") return null;
  if (!state.me) return <Navigate to="/login" replace />;
  return (
    <Navigate
      to={state.me.isAdmin ? "/jabali-admin" : "/jabali-panel"}
      replace
    />
  );
}
