// AuthContext.tsx — single source of identity state for the SPA,
// backed by TanStack Query so every consumer shares the same
// whoami cache. Replaces Refine's <Authenticated> / authProvider
// plumbing one layer at a time.
//
// Identity source: GET /api/v1/me. Same endpoint identity.ts already
// uses — panel-api's RequireKratosSession middleware validates the
// ory_kratos_session cookie upstream, resolves the Kratos identity
// to a panel user row, and returns { id (panel ULID), email, is_admin
// (DB-authoritative, not the Kratos trait) }.
//
// We DO NOT call Kratos /sessions/whoami directly. The Kratos
// identity UUID and the panel ULID are different values; any PATCH
// /users/:id or GET /users/:id/usage call needs the panel ULID.
// /me returns that — /sessions/whoami does not.
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  type ReactNode,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";
import { logoutBrowser } from "../kratos";

export type MeUser = {
  id: string;
  email: string;
  isAdmin: boolean;
};

type MeResponse = {
  id: string;
  email: string;
  is_admin: boolean;
};

type AuthState = {
  user: MeUser | null;
  isAdmin: boolean;
  isLoading: boolean;
  refresh: () => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

async function fetchMe(): Promise<MeUser | null> {
  try {
    const { data } = await apiClient.get<MeResponse>("/me");
    if (!data?.id) return null;
    return {
      id: data.id,
      email: data.email ?? "",
      isAdmin: data.is_admin === true,
    };
  } catch {
    // 401 (no session) and transient 5xx both collapse to null. Consumers
    // treat null as "not logged in" and RequireAuth redirects to /login.
    return null;
  }
}

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const qc = useQueryClient();

  const { data, isLoading, refetch } = useQuery<MeUser | null>({
    queryKey: ["whoami"],
    queryFn: fetchMe,
    retry: false,
    // whoami is the root of every protected render — keep it fresh
    // enough that role changes propagate without a hard reload, but
    // not so hungry that every tab focus hits the network.
    staleTime: 60_000,
  });

  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);

  const logout = useCallback(async () => {
    // Kratos browser-flow logout revokes the ory_kratos_session cookie.
    // Best-effort — a stale cookie or an unreachable Kratos shouldn't
    // block client-side cleanup.
    try {
      await logoutBrowser();
    } catch {
      /* noop */
    }
    qc.clear();
  }, [qc]);

  const value = useMemo<AuthState>(
    () => ({
      user: data ?? null,
      isAdmin: data?.isAdmin === true,
      isLoading,
      refresh,
      logout,
    }),
    [data, isLoading, refresh, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

export const useAuth = (): AuthState => {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
};
