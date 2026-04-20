// SearchableTable — a thin wrapper around AntD's Table that adds a
// debounced search input above it. Pagination behaviour matches AntD's
// stock Table (just page number buttons); callers that want a page-size
// chooser or total row count can pass `pagination={{ showSizeChanger:
// true, showTotal: … }}` explicitly.
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
// M21 transition note: pages migrated off Refine should use
// `<SearchableTableStringQ>` instead — same UX, plain `(q: string) =>
// void` callback. Kept here side-by-side so the Refine callers don't
// have to move in lockstep.
//
// Why debounce: each keystroke re-fires the list request through the
// data provider. 300ms is the usual sweet spot — responsive enough that
// it feels live, slow enough that typing "example.com" doesn't hit the
// server 11 times.
import { useEffect, useRef, useState } from "react";
import { Input, Space, Table } from "antd";
import { SearchOutlined } from "@ant-design/icons";
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

  // Pagination passes through untouched — stock AntD defaults (simple
  // page-number buttons, no size chooser, no total-row summary). Callers
  // that need those knobs can opt in explicitly.
  return (
    <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
      <Input
        placeholder={searchPlaceholder}
        prefix={<SearchOutlined />}
        allowClear
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        // Pressing Enter fires immediately (bypasses debounce timer).
        onPressEnter={() => {
          if (timer.current) clearTimeout(timer.current);
          const v = query.trim();
          if (v === "") {
            onSearchChange([]);
          } else {
            onSearchChange([{ field: "q", operator: "contains", value: v }]);
          }
        }}
        style={{ maxWidth: 360 }}
      />
      <Table<T> {...tableProps}>
        {tableProps.children}
      </Table>
    </Space>
  );
}

// ---------------------------------------------------------------------------
// String-query variant for post-M21 (Refine-free) callers. Same debounce
// + Enter-to-fire UX; emits a plain string so callers can feed it
// straight into useTableURL's setParams({ q }).
// ---------------------------------------------------------------------------

export interface SearchableTableStringQProps<T> extends TableProps<T> {
  onSearchChange: (q: string) => void;
  searchPlaceholder?: string;
  initialSearch?: string;
  debounceMs?: number;
}

export function SearchableTableStringQ<T extends object>({
  onSearchChange,
  searchPlaceholder = "Search…",
  initialSearch = "",
  debounceMs = 300,
  ...tableProps
}: SearchableTableStringQProps<T>) {
  const [query, setQuery] = useState(initialSearch);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      onSearchChange(query.trim());
    }, debounceMs);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, debounceMs]);

  return (
    <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
      <Input
        placeholder={searchPlaceholder}
        prefix={<SearchOutlined />}
        allowClear
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onPressEnter={() => {
          if (timer.current) clearTimeout(timer.current);
          onSearchChange(query.trim());
        }}
        style={{ maxWidth: 360 }}
      />
      <Table<T> {...tableProps}>{tableProps.children}</Table>
    </Space>
  );
}
