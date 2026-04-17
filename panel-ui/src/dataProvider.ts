// Custom Refine data provider speaking our panel's shape.
//
// We deliberately don't use @refinedev/simple-rest here — it expects
// ?_start=&_end= pagination and an X-Total-Count header on lists, both
// of which are orthogonal to what our Go handlers emit. A handwritten
// provider costs ~80 lines and avoids every "why's my total missing?"
// debugging session later.
//
// Contract:
//   getList    GET /resource?page=&page_size=&q=&sort=&order=
//              → { data: [...], total, page, page_size }
//   getOne     GET /resource/:id              → row
//   create     POST /resource                 → row
//   update     PATCH /resource/:id            → row
//   deleteOne  DELETE /resource/:id           → 204
//
// Search / sort:
//   - At most one filter is forwarded as ?q=. We pass the *value* of the
//     first non-empty filter; the server picks which columns to match
//     against from its own allowlist (repository.ListCols), so clients
//     can't target arbitrary DB columns through this channel.
//   - At most one sorter is forwarded. Refine supports multi-column
//     sort; our backend does not. First sorter wins, rest discarded.
//
// The apiClient already handles Authorization, refresh-on-401, and
// JSON parsing, so each handler here is essentially a one-liner.
import type {
  BaseKey,
  CrudFilters,
  CrudSorting,
  DataProvider,
} from "@refinedev/core";

import { apiClient } from "./apiClient";

const API_URL = "/api/v1";

type ListEnvelope<T> = {
  data: T[];
  total: number;
  page: number;
  page_size: number;
};

// Pull the first non-empty filter value. We don't honour Refine's `field`
// because the server decides which columns are searchable — clients only
// control the search string.
function pickSearch(filters?: CrudFilters): string | undefined {
  if (!filters || filters.length === 0) return undefined;
  for (const f of filters) {
    if ("value" in f && f.value != null && String(f.value).length > 0) {
      return String(f.value);
    }
  }
  return undefined;
}

// Pull the first sorter. Refine allows multi-column sort; our backend
// supports a single ORDER BY, so subsequent entries are discarded.
function pickSort(sorters?: CrudSorting): { sort?: string; order?: string } {
  if (!sorters || sorters.length === 0) return {};
  const s = sorters[0];
  return { sort: s.field, order: s.order };
}

export const dataProvider: DataProvider = {
  getApiUrl: () => API_URL,

  getList: async ({ resource, pagination, filters, sorters }) => {
    const page = pagination?.current ?? 1;
    const pageSize = pagination?.pageSize ?? 20;
    const q = pickSearch(filters);
    const { sort, order } = pickSort(sorters);

    const resp = await apiClient.get<ListEnvelope<unknown>>(`/${resource}`, {
      params: {
        page,
        page_size: pageSize,
        // Only send q/sort/order when set — keeps the network tab clean
        // and preserves historical defaults server-side.
        ...(q ? { q } : {}),
        ...(sort ? { sort } : {}),
        ...(order ? { order } : {}),
      },
    });
    return {
      data: resp.data.data as never,
      total: resp.data.total,
    };
  },

  getOne: async ({ resource, id }) => {
    const resp = await apiClient.get(`/${resource}/${encodeId(id)}`);
    return { data: resp.data };
  },

  create: async ({ resource, variables }) => {
    const resp = await apiClient.post(`/${resource}`, variables);
    return { data: resp.data };
  },

  update: async ({ resource, id, variables }) => {
    const resp = await apiClient.patch(`/${resource}/${encodeId(id)}`, variables);
    return { data: resp.data };
  },

  deleteOne: async ({ resource, id }) => {
    await apiClient.delete(`/${resource}/${encodeId(id)}`);
    // Refine requires a data field; 204 has no body, so echo the id.
    return { data: { id } as never };
  },
};

// encodeId keeps path-unsafe ids (should never happen for ULIDs, but cheap
// insurance) from silently producing 404s via URL mis-escape.
function encodeId(id: BaseKey): string {
  return encodeURIComponent(String(id));
}
