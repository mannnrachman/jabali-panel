// RequireUser.tsx — route guard for the user shell.
//
// Authenticated admins get bounced to the admin shell, matching the
// pre-M21 RoleGate behavior: an admin typing /jabali-panel manually
// shouldn't land in the user UI.
//
// Unauthenticated visitors go to /login (same as RequireAuth).
import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { Spin } from "antd";

import { useAuth } from "./AuthContext";

const ADMIN_HOME = "/jabali-admin";

export const RequireUser = ({ children }: { children: ReactNode }) => {
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

  if (isAdmin) {
    return <Navigate to={ADMIN_HOME} replace />;
  }

  return <>{children}</>;
};
