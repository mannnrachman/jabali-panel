// useCatchAll.ts — M6.5 domain-level catch-all hooks.
//
// Wire contract: GET/PUT/DELETE /domains/:id/catchall → {domain_id, domain_name, target, updated_at}
// Verified against panel-api/internal/api/domain_catchall.go (ADR-0051, DB-as-truth).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../apiClient";

export interface DomainCatchAll {
  domain_id: string;
  domain_name: string;
  target: string | null;
  updated_at: string;
}

const QK = (domainID: string) => ["catchall", domainID];

export function useDomainCatchAll(domainID: string) {
  return useQuery({
    queryKey: QK(domainID),
    queryFn: async () => {
      const { data } = await apiClient.get<DomainCatchAll>(
        `/domains/${domainID}/catchall`,
      );
      return data;
    },
    enabled: !!domainID,
  });
}

export function useUpdateDomainCatchAll() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ domainID, target }: { domainID: string; target: string }) => {
      const { data } = await apiClient.put<DomainCatchAll>(
        `/domains/${domainID}/catchall`,
        { target },
      );
      return data;
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: QK(data.domain_id) });
    },
  });
}

export function useDeleteDomainCatchAll() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (domainID: string) => {
      await apiClient.delete(`/domains/${domainID}/catchall`);
    },
    onSuccess: (_void, domainID) => {
      qc.invalidateQueries({ queryKey: QK(domainID) });
    },
  });
}
