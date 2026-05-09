// Hooks for M36 per-domain IP ACLs. Mirrors useMailboxes pattern:
// list/create/delete + cache invalidation on the per-domain key.
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type ACLAction = "allow" | "deny";

export type DomainIPACL = {
  id: string;
  domain_id: string;
  cidr: string;
  action: ACLAction;
  priority: number;
  comment: string;
  created_at: string;
};

export type CreateACLInput = {
  cidr: string;
  action: ACLAction;
  priority?: number;
  comment?: string;
};

const listKey = (domainId: string) => ["list", "domain-ip-acls", domainId];

export function useDomainIPACLs(
  domainId: string | undefined,
): UseQueryResult<{ data: DomainIPACL[]; total: number }> {
  return useQuery({
    queryKey: listKey(domainId ?? ""),
    queryFn: async () => {
      const { data } = await apiClient.get<{
        data: DomainIPACL[];
        total: number;
      }>(`/domains/${domainId}/acls`);
      return data;
    },
    enabled: !!domainId,
  });
}

export function useCreateDomainIPACL(
  domainId: string,
): UseMutationResult<DomainIPACL, unknown, CreateACLInput> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input) => {
      const { data } = await apiClient.post<DomainIPACL>(
        `/domains/${domainId}/acls`,
        input,
      );
      return data;
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: listKey(domainId) });
    },
  });
}

export function useDeleteDomainIPACL(
  domainId: string,
): UseMutationResult<void, unknown, { aclID: string }> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ aclID }) => {
      await apiClient.delete(`/domains/${domainId}/acls/${aclID}`);
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: listKey(domainId) });
    },
  });
}
