// useDisclaimer.ts — M6.5 Step 6 per-domain disclaimer hooks.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../apiClient";

export interface Disclaimer {
  domain_id: string;
  domain_name: string;
  enabled: boolean;
  text: string;
  updated_at: string;
}

const QK = (domainID: string) => ["disclaimer", domainID];

export function useDisclaimer(domainID: string) {
  return useQuery({
    queryKey: QK(domainID),
    queryFn: async () => {
      const { data } = await apiClient.get<Disclaimer>(`/domains/${domainID}/disclaimer`);
      return data;
    },
    enabled: !!domainID,
  });
}

export function useUpdateDisclaimer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      domainID,
      enabled,
      text,
    }: {
      domainID: string;
      enabled: boolean;
      text: string;
    }) => {
      const { data } = await apiClient.put<Disclaimer>(
        `/domains/${domainID}/disclaimer`,
        { enabled, text },
      );
      return data;
    },
    onSuccess: (data) => qc.invalidateQueries({ queryKey: QK(data.domain_id) }),
  });
}

export function useDeleteDisclaimer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (domainID: string) => {
      await apiClient.delete(`/domains/${domainID}/disclaimer`);
    },
    onSuccess: (_v, id) => qc.invalidateQueries({ queryKey: QK(id) }),
  });
}
