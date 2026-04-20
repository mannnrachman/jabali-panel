// LandingRedirect — the "where should I go?" dispatcher for any URL
// that isn't pre-committed to a shell. Used by:
//   /                          — bare-root entry after login or bookmark
//   /login (while logged in)   — bounced forward instead of seeing the form again
//   * (catch-all)              — unknown URLs after auth
//
// Reads identity from the shared whoami cache (populated by
// AuthProvider), dispatches by role. Full-page Spin while the first
// whoami is in flight — matches RequireAuth's R3 mitigation from
// ADR-0037 so cold loads don't briefly show /login before redirect.
import { Navigate } from "react-router";
import { Spin } from "antd";

import { useAuth } from "../auth/AuthContext";

export function LandingRedirect() {
  const { user, isLoading } = useAuth();

  if (isLoading) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: "100vh",
        }}
      >
        <Spin size="large" />
      </div>
    );
  }

  if (!user) return <Navigate to="/login" replace />;
  return (
    <Navigate
      to={user.isAdmin ? "/jabali-admin" : "/jabali-panel"}
      replace
    />
  );
}
