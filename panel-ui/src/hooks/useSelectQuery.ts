// useSelectQuery.ts — options provider for <Select> and <AutoComplete>.
//
// Replaces Refine's `useSelect` without the provider chain. Fetches a
// resource list once (pageSize: 100 by default) and shapes it into the
// { label, value } tuples AntD's Select consumes. For larger pickers,
// the caller should keep using a full table + modal instead of this.
import { useMemo } from "react";

import {
  useListQuery,
  type ListParams,
  type UseListQueryResult,
} from "./useQueries";

export type SelectOption = {
  label: string;
  value: string;
};

export type UseSelectQueryResult<T> = UseListQueryResult<T> & {
  options: SelectOption[];
};

export function useSelectQuery<T extends Record<string, unknown>>({
  resource,
  labelField,
  valueField = "id",
  pageSize = 100,
  extraParams,
  enabled = true,
}: {
  resource: string;
  labelField: keyof T & string;
  valueField?: keyof T & string;
  pageSize?: number;
  extraParams?: ListParams;
  enabled?: boolean;
}): UseSelectQueryResult<T> {
  const query = useListQuery<T>({
    resource,
    params: { pageSize, ...extraParams },
    enabled,
  });

  const options = useMemo<SelectOption[]>(
    () =>
      query.items.map((item) => ({
        label: String(item[labelField] ?? ""),
        value: String(item[valueField] ?? ""),
      })),
    [query.items, labelField, valueField],
  );

  return { ...query, options };
}
