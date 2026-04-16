// Custom Refine data provider speaking our panel's shape.
//
// We deliberately don't use @refinedev/simple-rest here — it expects
// ?_start=&_end= pagination and an X-Total-Count header on lists, both
// of which are orthogonal to what our Go handlers emit. A handwritten
// provider costs ~60 lines and avoids every "why's my total missing?"
// debugging session later.
//
// Contract:
//   getList    GET /resource?page=&page_size=&_sort=&_order=
//              → { data: [...], total, page, page_size }
//   getOne     GET /resource/:id              → row
//   create     POST /resource                 → row
//   update     PATCH /resource/:id            → row
//   deleteOne  DELETE /resource/:id           → 204
//
// The apiClient already handles Authorization, refresh-on-401, and
// JSON parsing, so each handler here is essentially a one-liner.
import type { BaseKey, DataProvider } from "@refinedev/core";

import { apiClient } from "./apiClient";

const API_URL = "/api/v1";

type ListEnvelope<T> = {
  data: T[];
  total: number;
  page: number;
  page_size: number;
};

export const dataProvider: DataProvider = {
  getApiUrl: () => API_URL,

  getList: async ({ resource, pagination }) => {
    // Sort / filter params are deliberately NOT forwarded. Our API
    // currently returns rows in created_at DESC order with no filter
    // support; adding the params to the wire would just be cosmetic
    // noise in the network tab. When the server grows ORDER BY + WHERE
    // support, re-introduce sorter/filter handling here.
    const page = pagination?.current ?? 1;
    const pageSize = pagination?.pageSize ?? 20;

    const resp = await apiClient.get<ListEnvelope<unknown>>(`/${resource}`, {
      params: { page, page_size: pageSize },
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
