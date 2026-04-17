// SearchableTable — a thin wrapper around AntD's Table that adds a
// debounced search input above it and standard pagination controls
// (showSizeChanger, showTotal, pageSizeOptions).
//
// Meant to be dropped in wherever a list page currently renders
// <Table {...tableProps} />. The caller wires it up by:
//
//   1. calling Refine's useTable({ resource, syncWithLocation: true })
//      with `filters: { initial: [{ field: "q", operator: "contains", value: "" }] }`
//      or any equivalent LogicalFilter — the only thing our backend cares
//      about is the *value*, since the server picks columns from an
//      allowlist.
//   2. passing the hook's `tableProps` + the `setFilters` callback here.
//
// Why debounce: each keystroke re-fires the list request through the
// data provider. 300ms is the usual sweet spot — responsive enough that
// it feels live, slow enough that typing "example.com" doesn't hit the
// server 11 times.
import { useEffect, useMemo, useRef, useState } from "react";
import { Input, Space, Table } from "antd";
import type { TableProps } from "antd";
import type { CrudFilters } from "@refinedev/core";

export interface SearchableTableProps<T> extends TableProps<T> {
  /** Called when the user types in the search box. */
  onSearchChange: (filters: CrudFilters) => void;
  searchPlaceholder?: string;
  /** Initial value to seed the input when coming in via URL ?filters=…. */
  initialSearch?: string;
  /** Milliseconds to debounce before firing onSearchChange. Default 300. */
  debounceMs?: number;
}

/**
 * Drop-in Table replacement that adds search + pagination polish.
 * Generic keeps AntD's column inference working.
 */
export function SearchableTable<T extends object>({
  onSearchChange,
  searchPlaceholder = "Search…",
  initialSearch = "",
  debounceMs = 300,
  pagination,
  ...tableProps
}: SearchableTableProps<T>) {
  const [query, setQuery] = useState(initialSearch);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      // Empty query ⇒ clear filters so the server sees no ?q= param.
      if (query.trim() === "") {
        onSearchChange([]);
      } else {
        onSearchChange([
          { field: "q", operator: "contains", value: query.trim() },
        ]);
      }
    }, debounceMs);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
    // onSearchChange intentionally omitted — Refine's setFilters identity
    // is stable per resource, and including it would reset the debounce
    // every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, debounceMs]);

  // Merge caller's pagination config (if any) with our standard defaults.
  // `pagination === false` means "caller wants no pagination"; respect it.
  const mergedPagination = useMemo(() => {
    if (pagination === false) return false as const;
    return {
      showSizeChanger: true,
      pageSizeOptions: ["10", "20", "50", "100"],
      showTotal: (total: number, range: [number, number]) =>
        `${range[0]}–${range[1]} of ${total}`,
      ...(pagination ?? {}),
    };
  }, [pagination]);

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Input.Search
        placeholder={searchPlaceholder}
        allowClear
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        // Pressing Enter fires immediately (bypasses debounce timer).
        onSearch={(v) => {
          if (timer.current) clearTimeout(timer.current);
          if (v.trim() === "") {
            onSearchChange([]);
          } else {
            onSearchChange([
              { field: "q", operator: "contains", value: v.trim() },
            ]);
          }
        }}
        style={{ maxWidth: 360 }}
      />
      <Table<T> {...tableProps} pagination={mergedPagination}>
        {tableProps.children}
      </Table>
    </Space>
  );
}

