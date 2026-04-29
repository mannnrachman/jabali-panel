// useUserEgress — TanStack Query wrappers for the M34 per-user egress
// firewall endpoints. Two surfaces share the same hook file: admin
// (/admin/users/:id/egress) and self (/me/egress).
//
// Backed by panel-api/internal/api/user_egress.go (verified against
// handler — feedback_verify_wire_contract).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type EgressState = "off" | "learning" | "enforced";
export type EgressProtocol = "tcp" | "udp";
export type EgressRequestStatus = "pending" | "approved" | "denied";

export interface EgressDestination {
  cidr: string;
  port?: number;
  protocol?: EgressProtocol;
  comment?: string;
}

export interface EgressPolicy {
  user_id: string;
  state: EgressState;
  allowed_extra: EgressDestination[];
  drop_count_24h: number;
  drop_count_at?: string;
  learning_started_at?: string;
  updated_at: string;
  updated_by?: string;
}

export interface EgressRequest {
  id: string;
  user_id: string;
  cidr: string;
  port?: number;
  protocol: EgressProtocol;
  reason: string;
  status: EgressRequestStatus;
  reviewed_by?: string;
  decided_at?: string;
  created_at: string;
}

export interface EgressSummary {
  state_counts: Record<EgressState, number>;
  total_drops: number;
  policy_total: number;
}

const keys = {
  policy: (userID: string) => ["user-egress", "policy", userID] as const,
  me: ["user-egress", "me"] as const,
  meRequests: ["user-egress", "me", "requests"] as const,
  pendingRequests: ["user-egress", "requests", "pending"] as const,
  summary: ["user-egress", "summary"] as const,
};

// Admin: fetch one user's policy.
export function useUserEgressPolicy(userID: string | undefined) {
  return useQuery<EgressPolicy>({
    queryKey: keys.policy(userID ?? ""),
    queryFn: async () => {
      const res = await apiClient.get<EgressPolicy>(`/admin/users/${userID}/egress`);
      return res.data;
    },
    enabled: !!userID,
  });
}

// Admin: replace one user's policy.
export function useUpdateUserEgress(userID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (payload: {
      state: EgressState;
      allowed_extra: EgressDestination[];
    }): Promise<EgressPolicy> => {
      const res = await apiClient.put<EgressPolicy>(`/admin/users/${userID}/egress`, payload);
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["user-egress"] });
    },
  });
}

// Admin: list pending requests.
export function usePendingEgressRequests() {
  return useQuery<{ data: EgressRequest[]; total: number }>({
    queryKey: keys.pendingRequests,
    queryFn: async () => {
      const res = await apiClient.get<{ data: EgressRequest[]; total: number }>(
        `/admin/egress-requests`,
      );
      return res.data;
    },
    refetchInterval: 60_000,
  });
}

// Admin: approve a request (and fold the destination into the user's
// allowed_extra). Server is idempotent — already-decided rows return 409.
export function useDecideEgressRequest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: { id: string; decision: "approve" | "deny" }) => {
      const res = await apiClient.post(
        `/admin/egress-requests/${args.id}/${args.decision}`,
      );
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["user-egress"] });
    },
  });
}

// Admin: aggregate widget feed.
export function useEgressSummary() {
  return useQuery<EgressSummary>({
    queryKey: keys.summary,
    queryFn: async () => {
      const res = await apiClient.get<EgressSummary>(`/admin/egress-summary`);
      return res.data;
    },
    refetchInterval: 60_000,
  });
}

// User: own policy.
export function useMeEgress() {
  return useQuery<EgressPolicy>({
    queryKey: keys.me,
    queryFn: async () => {
      const res = await apiClient.get<EgressPolicy>(`/me/egress`);
      return res.data;
    },
  });
}

// User: own request history.
export function useMeEgressRequests() {
  return useQuery<{ data: EgressRequest[]; total: number }>({
    queryKey: keys.meRequests,
    queryFn: async () => {
      const res = await apiClient.get<{ data: EgressRequest[]; total: number }>(
        `/me/egress/requests`,
      );
      return res.data;
    },
  });
}

// User: submit a destination request.
export function useSubmitEgressRequest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (payload: {
      cidr: string;
      port?: number;
      protocol?: EgressProtocol;
      reason: string;
    }): Promise<EgressRequest> => {
      const res = await apiClient.post<EgressRequest>(`/me/egress/request`, payload);
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.meRequests });
      qc.invalidateQueries({ queryKey: keys.pendingRequests });
    },
  });
}

// User: cancel a still-pending request.
export function useCancelMyEgressRequest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await apiClient.delete(`/me/egress/requests/${id}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.meRequests });
    },
  });
}
