// SearchableTable — a thin wrapper around AntD's Table that adds a
// debounced search input above it. Pagination behaviour matches AntD's
// stock Table (just page number buttons); callers that want a page-size
// chooser or total row count can pass `pagination={{ showSizeChanger:
// true, showTotal: … }}` explicitly.
//
// Post-M21 this module exposes a single, Refine-free variant
// `<SearchableTableStringQ>`. The old Refine-shaped CrudFilters
// variant was removed in Wave E once every caller moved to
// useTableURL + setParams({ q }).
//
// Why debounce: each keystroke re-fires the list request through the
// data hook. 300ms is the usual sweet spot — responsive enough that
// it feels live, slow enough that typing "example.com" doesn't hit
// the server 11 times.
import { useEffect, useRef, useState } from "react";
import { Input, Space, Table } from "antd";
import type { TableProps } from "antd";

export interface SearchableTableStringQProps<T> extends TableProps<T> {
  /** Called with the current search query (empty string clears). */
  onSearchChange: (q: string) => void;
  searchPlaceholder?: string;
  /** Initial value to seed the input when coming in via URL ?q=…. */
  initialSearch?: string;
  /** Milliseconds to debounce before firing onSearchChange. Default 300. */
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

  // Default scroll.x = "max-content" so wide tables scroll horizontally
  // inside the Card on narrow viewports instead of clipping at the
  // viewport edge. Merge property-by-property so a caller that passes
  // scroll={{ y: 300 }} (virtual table) keeps that value — naive
  // spread would replace the whole scroll object and lose y. See
  // ADR-0046.
  const { scroll: callerScroll, ...restTableProps } = tableProps;
  const scroll = {
    x: "max-content" as const,
    ...(callerScroll ?? {}),
  };

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      onSearchChange(query.trim());
    }, debounceMs);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
    // onSearchChange intentionally omitted — callers typically pass
    // an inline arrow function whose identity changes every render,
    // and including it here would reset the debounce on each
    // keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, debounceMs]);

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Input.Search
        placeholder={searchPlaceholder}
        allowClear
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        // onSearch fires on Enter or on search-button click — bypasses
        // the debounce timer since the user just asked for "now".
        onSearch={(value) => {
          if (timer.current) clearTimeout(timer.current);
          onSearchChange(value.trim());
        }}
        style={{ maxWidth: 360 }}
      />
      <Table<T> {...restTableProps} scroll={scroll}>
        {restTableProps.children}
      </Table>
    </Space>
  );
}
