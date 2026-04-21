// UserDomainList — tenant view of domains they own. Same row-action
// strip as the admin list (DNS/Redirects/Index/Settings/Toggle/Delete)
// minus the Edit button (users edit per-domain config via the row
// buttons rather than a full edit page).
import { PlusSquareOutlined, GlobalOutlined } from "@ant-design/icons";
import { Button, Card, Space, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";
import type { SorterResult } from "antd/es/table/interface";

import { RowDeleteButton } from "../../../components/RowDeleteButton";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useDeleteMutation } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
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

const getSSLTagColor = (state?: string): string => {
  if (state === "active") return "green";
  if (state === "error") return "red";
  return "default";
};

const getSSLTagLabel = (state?: string): string => {
  return state || "off";
};

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  redirect_all_to?: string | null;
  redirect_all_type?: string | null;
  page_redirects?:
    | { source: string; destination: string; type: "301" | "302" | "307" | "308" }[]
    | null;
  index_priority?:
    | "html_first"
    | "php_first"
    | "html_only"
    | "php_only"
    | "full"
    | null;
  ssl_state?: string;
  created_at: string;
  updated_at: string;
};

export const UserDomainList = () => {
  const navigate = useNavigate();
  const query = useTableURL<Domain>({
    resource: "domains",
    defaultSort: "name",
    defaultOrder: "asc",
  });
  const deleteMutation = useDeleteMutation({ resource: "domains" });

  const handleTableChange: React.ComponentProps<typeof Table<Domain>>["onChange"] = (
    pagination,
    _filters,
    sorter,
  ) => {
    const single = Array.isArray(sorter)
      ? (sorter[0] as SorterResult<Domain> | undefined)
      : (sorter as SorterResult<Domain>);
    query.setParams({
      page: pagination.current ?? 1,
      pageSize: pagination.pageSize ?? 20,
      sort: single?.columnKey ? String(single.columnKey) : undefined,
      order:
        single?.order === "ascend"
          ? "asc"
          : single?.order === "descend"
            ? "desc"
            : undefined,
    });
  };

  return (
    <div>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          My Domains
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => navigate("/jabali-panel/domains/create")}
        >
          Add Domain
        </Button>
      </Space>

      <Card>
        <SearchableTableStringQ<Domain>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search by domain name"
          onSearchChange={(q) => query.setParams({ q, page: 1 })}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
          }}
          onChange={handleTableChange}
        >
          <Table.Column<Domain>
            dataIndex="name"
            title="Domain"
            key="name"
            sorter={{ multiple: 1 }}
            defaultSortOrder="ascend"
            render={(name: string, record: Domain) =>
              renderDomainCell(name, record.doc_root)
            }
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
            dataIndex="ssl_state"
            title="SSL"
            render={(state?: string) => (
              <Tag color={getSSLTagColor(state)}>{getSSLTagLabel(state)}</Tag>
            )}
          />
          <Table.Column<Domain>
            title="Actions"
            dataIndex="actions"
            render={(_, r) => (
              <Space>
                <Button
                  type="text"
                  icon={<GlobalOutlined />}
                  onClick={() =>
                    navigate(`/jabali-panel/domains/${r.id}/dns`)
                  }
                >
                  DNS
                </Button>
                <DomainRedirectsButton domain={r} />
                <DomainIndexButton domain={r} />
                <DomainSettingsButton domain={r} />
                <DomainToggleButton domain={r} />
                <RowDeleteButton
                  confirmTitle={`Delete domain "${r.name}"?`}
                  onConfirm={async () => {
                    await deleteMutation.mutateAsync({ id: r.id });
                  }}
                />
              </Space>
            )}
          />
        </SearchableTableStringQ>
      </Card>
    </div>
  );
};
