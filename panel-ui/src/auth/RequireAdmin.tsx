// RequireAdmin.tsx — route guard for admin-only pages.
//
// Composes RequireAuth (so we still redirect to /login when not
// signed in) and additionally bounces non-admins to the user shell.
// Matches the RoleGate pattern the current app uses: authenticated
// but wrong role → cross-shell redirect, not a 403.
import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { Spin } from "antd";

import { useAuth } from "./AuthContext";

const USER_HOME = "/jabali-panel";

export const RequireAdmin = ({ children }: { children: ReactNode }) => {
  const { user, isAdmin, isLoading } = useAuth();

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

  if (!user) {
    return <Navigate to="/login" replace />;
  }

  if (!isAdmin) {
    return <Navigate to={USER_HOME} replace />;
  }

  return <>{children}</>;
};
