// RequireAuth.tsx — route guard for authenticated-only pages.
//
// While the whoami query is in flight, we show a full-page spinner
// instead of rendering the (unauthenticated) children. That's the
// R3 mitigation from ADR-0037: without this guard cold loads flash
// the protected layout for ~100ms before the redirect fires.
import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { Spin } from "antd";

import { useAuth } from "./AuthContext";

export const RequireAuth = ({ children }: { children: ReactNode }) => {
  const { user, isLoading } = useAuth();
  const location = useLocation();

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
    // Preserve the attempted URL so /login can bounce back here after
    // a successful sign-in. Login.tsx already reads `from` off the
    // location state.
    return (
      <Navigate to="/login" replace state={{ from: location.pathname }} />
    );
  }

  return <>{children}</>;
};
