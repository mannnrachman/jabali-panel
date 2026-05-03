// useSecurityTrust — TanStack Query hook for the M43 trust test
// bench. Wire contract per panel-api/internal/api/security_trust.go.
import { useMutation } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type TrustVerdict = {
  layer: string;
  outcome: "allow" | "deny" | "unknown";
  detail: string;
};

export type TrustTestResponse = {
  ip: string;
  verdicts: TrustVerdict[];
};

const BASE = "/admin/security/trust";

export function useTrustTest() {
  return useMutation({
    mutationFn: async (ip: string): Promise<TrustTestResponse> => {
      const { data } = await apiClient.post<TrustTestResponse>(`${BASE}/test`, { ip });
      return data;
    },
  });
}
