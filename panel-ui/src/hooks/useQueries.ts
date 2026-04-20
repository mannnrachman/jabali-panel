// useQueries.ts — thin TanStack Query hooks shaped for panel-api's REST
// conventions. Purpose: a drop-in replacement for Refine's useList /
// useOne / useCreate / useUpdate / useDelete without the Refine
// provider chain.
//
// Conventions (match panel-api):
//   - List:   GET    /api/v1/{resource}?page=&pageSize=&q=&sort=&order=
//             → { items: T[], total: number }
//   - One:    GET    /api/v1/{resource}/{id}  → T
//   - Create: POST   /api/v1/{resource}        → T
//   - Update: PATCH  /api/v1/{resource}/{id}   → T
//   - Delete: DELETE /api/v1/{resource}/{id}   → 204
//
// Cache keys are stable tuples so we can invalidate surgically:
//   ["list", resource, params] — list-scoped, params-aware
//   ["one",  resource, id]     — single-record
//
// On create/update/delete we invalidate the list root (["list", resource])
// which matches ALL paginated/filtered variants. Update additionally
// invalidates ["one", resource, id].
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { apiClient } from "../apiClient";

// ---------------------------------------------------------------------------
// List query
// ---------------------------------------------------------------------------

export type ListParams = {
  page?: number;
  pageSize?: number;
  q?: string;
  sort?: string;
  order?: "asc" | "desc";
  // Extra filter keys panel-api accepts (e.g. is_admin=true on /users).
  // Unknown keys are forwarded as query params as-is.
  [key: string]: string | number | boolean | undefined;
};

export type ListResponse<T> = {
  items: T[];
  total: number;
};

export type UseListQueryResult<T> = UseQueryResult<ListResponse<T>> & {
  items: T[];
  total: number;
};

function toQueryString(params: ListParams): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export function useListQuery<T>({
  resource,
  params = {},
  enabled = true,
}: {
  resource: string;
  params?: ListParams;
  enabled?: boolean;
}): UseListQueryResult<T> {
  const q = useQuery({
    queryKey: ["list", resource, params],
    queryFn: async () => {
      const { data } = await apiClient.get<ListResponse<T>>(
        `/${resource}${toQueryString(params)}`,
      );
      return data;
    },
    enabled,
  });
  return {
    ...q,
    items: q.data?.items ?? [],
    total: q.data?.total ?? 0,
  };
}

// ---------------------------------------------------------------------------
// One query
// ---------------------------------------------------------------------------

export function useOneQuery<T>({
  resource,
  id,
  enabled = true,
}: {
  resource: string;
  id: string | undefined;
  enabled?: boolean;
}): UseQueryResult<T> {
  return useQuery({
    queryKey: ["one", resource, id],
    queryFn: async () => {
      const { data } = await apiClient.get<T>(`/${resource}/${id}`);
      return data;
    },
    enabled: enabled && !!id,
  });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

export function useCreateMutation<T, Input = Partial<T>>({
  resource,
}: {
  resource: string;
}): UseMutationResult<T, unknown, Input> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: Input) => {
      const { data } = await apiClient.post<T>(`/${resource}`, input);
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["list", resource] });
    },
  });
}

export function useUpdateMutation<T, Input = Partial<T>>({
  resource,
}: {
  resource: string;
}): UseMutationResult<T, unknown, { id: string; input: Input }> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }) => {
      const { data } = await apiClient.patch<T>(`/${resource}/${id}`, input);
      return data;
    },
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["list", resource] });
      qc.invalidateQueries({ queryKey: ["one", resource, id] });
    },
  });
}

export function useDeleteMutation({
  resource,
}: {
  resource: string;
}): UseMutationResult<void, unknown, { id: string }> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id }) => {
      await apiClient.delete(`/${resource}/${id}`);
    },
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["list", resource] });
      qc.invalidateQueries({ queryKey: ["one", resource, id] });
    },
  });
}
