import { useTable } from "@refinedev/antd";
import { DeleteButton, EditButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { Button, Space, Table, Tag, Typography } from "antd";
import { GlobalOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router";

import { DomainToggleButton } from "../../DomainToggleButton";
import { DomainSettingsButton } from "../../DomainSettingsButton";
import { DomainRedirectsButton } from "../../DomainRedirectsButton";
import { DomainIndexButton } from "../../DomainIndexButton";

const stripHomePrefix = (path: string): string => {
  if (path.startsWith("/home/")) {
    const match = path.match(/^\/home\/[^/]+\/(.*)/);
    return match ? match[1] : path;
  }
  return path;
};

const renderDomainCell = (name: string, docRoot: string) => (
  <div>
    <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
      <GlobalOutlined />
      <span style={{ fontWeight: 500 }}>{name}</span>
    </div>
    <div style={{ color: "#999", fontSize: "12px" }}>{stripHomePrefix(docRoot)}</div>
  </div>
);

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  ssl_enabled?: boolean;
  nginx_custom_directives: string;
  redirect_all_to?: string | null;
  redirect_all_type?: string | null;
  page_redirects?: { source: string; destination: string; type: "301" | "302" | "307" | "308" }[] | null;
  index_priority?: "html_first" | "php_first" | "html_only" | "php_only" | "full" | null;
  created_at: string;
  updated_at: string;
};

export const DomainList = () => {
  const navigate = useNavigate();
  const { tableProps, setFilters, filters } = useTable<Domain>({
    resource: "domains",
    syncWithLocation: true,
  });
  const initialSearch = readQValue(filters);

  return (
    <div style={{ padding: 24 }}>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          Domains
        </Typography.Title>
      </Space>

      <SearchableTable<Domain>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by domain name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<Domain>
          dataIndex="name"
          title="Domain"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(name: string, record: Domain) => renderDomainCell(name, record.doc_root)}
        />
        <Table.Column<Domain>
          dataIndex="user_id"
          title="User ID"
          render={(value: string) => value.substring(0, 8)}
        />
        <Table.Column<Domain>
          dataIndex="is_enabled"
          title="Status"
          render={(enabled: boolean) =>
            enabled ? (
              <Tag color="green">active</Tag>
            ) : (
              <Tag>disabled</Tag>
            )
          }
        />
        <Table.Column<Domain>
          dataIndex="ssl_enabled"
          title="SSL"
          render={(on?: boolean) =>
            on ? <Tag color="green">on</Tag> : <Tag>off</Tag>
          }
        />
        <Table.Column<Domain>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => (
            <Space size="small">
              <Button
                type="text"
                size="small"
                icon={<GlobalOutlined />}
                onClick={() => navigate(`/jabali-admin/domains/${r.id}/dns`)}
              >
                DNS
              </Button>
              <DomainRedirectsButton domain={r} />
              <DomainIndexButton domain={r} />
              <DomainSettingsButton domain={r} />
              <DomainToggleButton domain={r} />
              <EditButton hideText size="small" type="text" recordItemId={r.id} />
              <DeleteButton hideText size="small" type="text" resource="domains" recordItemId={r.id} />
            </Space>
          )}
        />
      </SearchableTable>
    </div>
  );
};
