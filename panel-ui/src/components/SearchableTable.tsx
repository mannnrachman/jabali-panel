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
import { SearchOutlined } from "@ant-design/icons";
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
          onSearchChange(query.trim());
        }}
        style={{ maxWidth: 360 }}
      />
      <Table<T> {...tableProps}>{tableProps.children}</Table>
    </Space>
  );
}
