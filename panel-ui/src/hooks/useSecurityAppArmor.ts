// useSecurityAppArmor — TanStack Query hooks for the M40 AppArmor
// admin endpoints. Wire contract per panel-api/internal/api/security_apparmor.go.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface AppArmorProfile {
  name: string;
  mode: "enforce" | "complain";
}

export interface AppArmorStatus {
  enabled: boolean;
  reason?: string;
  profiles: AppArmorProfile[];
}

// apiClient baseURL is already "/api/v1" — paths must be relative
// to that, NOT include the prefix again (would produce
// /api/v1/api/v1/... → 404).
const BASE = "/admin/security/apparmor";

export function useAppArmorStatus() {
  return useQuery({
    queryKey: ["security", "apparmor", "status"],
    queryFn: async () => {
      const { data } = await apiClient.get<AppArmorStatus>(`${BASE}/status`);
      return data;
    },
    refetchInterval: 60_000,
  });
}

export function useSetAppArmorMode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: { profile: string; mode: "enforce" | "complain" }) => {
      const { data } = await apiClient.post(
        `${BASE}/profiles/${args.profile}/mode`,
        { mode: args.mode },
      );
      return data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["security", "apparmor"] }),
  });
}
