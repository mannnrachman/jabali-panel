// useSecurityAide — TanStack Query hooks for the M42 AIDE FIM
// admin endpoints. Wire contract per panel-api/internal/api/security_aide.go.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface AideSampleRow {
  path: string;
  change_type: "added" | "changed" | "removed";
}

export interface AideStatus {
  enabled: boolean;
  reason?: string;
  db_age_seconds: number;
  last_check_ts?: string;
  summary: {
    added: number;
    changed: number;
    removed: number;
  };
  sample: AideSampleRow[];
}

// apiClient baseURL is already "/api/v1" — paths must be relative
// to that, NOT include the prefix again (would produce
// /api/v1/api/v1/... → 404).
const BASE = "/admin/security/aide";

export function useAideStatus() {
  return useQuery({
    queryKey: ["security", "aide", "status"],
    queryFn: async () => {
      const { data } = await apiClient.get<AideStatus>(`${BASE}/status`);
      return data;
    },
    refetchInterval: 60_000,
  });
}

export function useRunAideCheck() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { data } = await apiClient.post<AideStatus>(`${BASE}/check`);
      return data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["security", "aide"] }),
  });
}
