// columnSearchProps — returns the { filterIcon, filterDropdown } pair
// so list-page Table.Columns can expose a SearchOutlined icon in their
// header that opens a popover <Input.Search> wired to the list page's
// global `q` param (useTableURL).
//
// All affected tables share a single server-side `q` that fulltext-
// matches the allowlisted columns for their resource, so the popover
// is a UX affordance — not per-column exclusive filtering. The icon
// turns red while `q` is non-empty so it doubles as a "filter active"
// cue.
import { SearchOutlined } from "@ant-design/icons";
import { Input } from "antd";
import type { ColumnType } from "antd/es/table";

type Args = {
  placeholder: string;
  currentQ: string | undefined;
  onSearch: (value: string) => void;
};

export function columnSearchProps<T>({
  placeholder,
  currentQ,
  onSearch,
}: Args): Pick<ColumnType<T>, "filterIcon" | "filterDropdown"> {
  return {
    filterIcon: () => (
      <SearchOutlined style={{ color: currentQ ? "#ef4444" : undefined }} />
    ),
    filterDropdown: ({ confirm, close }) => (
      <div style={{ padding: 8, minWidth: 240 }}>
        <Input.Search
          placeholder={placeholder}
          allowClear
          defaultValue={currentQ}
          onSearch={(value) => {
            onSearch(value.trim());
            confirm({ closeDropdown: false });
            close();
          }}
        />
      </div>
    ),
  };
}
