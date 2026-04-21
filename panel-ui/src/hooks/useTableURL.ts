// useTableURL.ts — URL-backed table state (page/pageSize/q/sort/order)
// composed on top of useListQuery. Replaces Refine's `useTable` without
// the provider chain.
//
// The URL is the source of truth: the back button, a refresh, and a
// shared link all land on the same list view. `setParams` merges a
// patch into the existing query string; empty/undefined/null values
// delete the key so `?q=` and `?q` don't leak into the URL.
import { useSearchParams } from "react-router";

import {
  useListQuery,
  type ListParams,
  type UseListQueryResult,
} from "./useQueries";

export type TableParams = {
  page: number;
  pageSize: number;
  q: string;
  sort: string | undefined;
  order: "asc" | "desc";
};

export type UseTableURLResult<T> = UseListQueryResult<T> & {
  params: TableParams;
  setParams: (patch: Partial<TableParams>) => void;
};

export function useTableURL<T>({
  resource,
  defaultSort,
  defaultOrder = "desc",
  defaultPageSize = 20,
  extraParams,
}: {
  resource: string;
  defaultSort?: string;
  defaultOrder?: "asc" | "desc";
  defaultPageSize?: number;
  // Extra params forwarded to useListQuery (e.g. is_admin=true filter
  // on /users). Not URL-backed — caller drives them.
  extraParams?: ListParams;
}): UseTableURLResult<T> {
  const [searchParams, setSearchParams] = useSearchParams();

  const rawOrder = searchParams.get("order");
  const order: "asc" | "desc" =
    rawOrder === "asc" || rawOrder === "desc" ? rawOrder : defaultOrder;

  const params: TableParams = {
    page: Number(searchParams.get("page") ?? 1) || 1,
    pageSize: Number(searchParams.get("pageSize") ?? defaultPageSize) || defaultPageSize,
    q: searchParams.get("q") ?? "",
    sort: searchParams.get("sort") ?? defaultSort,
    order,
  };

  const queryParams: ListParams = {
    ...extraParams,
    page: params.page,
    pageSize: params.pageSize,
  };
  if (params.q) queryParams.q = params.q;
  if (params.sort) {
    queryParams.sort = params.sort;
    queryParams.order = params.order;
  }

  const query = useListQuery<T>({ resource, params: queryParams });

  const setParams = (patch: Partial<TableParams>) => {
    const next = new URLSearchParams(searchParams);
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === null || v === "") {
        next.delete(k);
      } else {
        next.set(k, String(v));
      }
    }
    // Strip default values so they don't pollute the URL — page=1,
    // pageSize=defaultPageSize, order=defaultOrder are implied by the
    // hook's contract and reproducing them as query-string noise was
    // the e2e flake root cause: AntD Table's onChange fires on initial
    // mount with the default pagination, and the resulting
    // setSearchParams({page:1}) was landing AFTER a pending
    // navigate('/create' or '/edit/:id'), clobbering the URL back to
    // '/users?page=1' and unmounting the target page. Stripping
    // defaults turns the initial-mount onChange into a no-op (see the
    // equality guard below). See c830630 and 599c9b2 for the
    // e2e-side workarounds; this is the product-side root-cause fix.
    if (next.get("page") === "1") next.delete("page");
    if (next.get("pageSize") === String(defaultPageSize)) next.delete("pageSize");
    if (next.get("order") === defaultOrder) next.delete("order");
    // No-op guard: don't call setSearchParams when the result is
    // identical to the current URL — that still triggers a history
    // push on some react-router versions and can race with in-flight
    // navigate() calls in a Link/onClick handler.
    if (next.toString() === searchParams.toString()) {
      return;
    }
    setSearchParams(next);
  };

  return {
    ...query,
    params,
    setParams,
  };
}
