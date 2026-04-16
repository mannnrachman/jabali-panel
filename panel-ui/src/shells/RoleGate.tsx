// RoleGate — lightweight role-based redirect for the shell entrypoints.
//
// Wraps a subtree and only lets it render if the current identity matches
// the `require` prop. Non-matching identities get bounced to their shell
// home. No identity → login. This is the *only* thing preventing an
// admin from seeing user-panel pages and vice versa; we don't rely on
// server-side 403s alone so the user's URL bar never briefly shows a
// screen they shouldn't see.
//
// Loading state: a blank page while getIdentity resolves. Intentional —
// a flash of the wrong shell is worse than a 100ms blank.
import { useEffect, useState, type ReactNode } from "react";
import { Navigate } from "react-router";

import { getIdentity, type Identity } from "../identity";

type RoleGateProps = {
  require: "admin" | "user";
  children: ReactNode;
};

export function RoleGate({ require, children }: RoleGateProps) {
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

  const ok =
    (require === "admin" && state.me.isAdmin) ||
    (require === "user" && !state.me.isAdmin);

  if (!ok) {
    return <Navigate to={state.me.isAdmin ? "/jabali-admin" : "/jabali-panel"} replace />;
  }

  return <>{children}</>;
}
