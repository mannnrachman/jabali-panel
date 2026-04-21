// DomainList — admin domains grid. Post-M21 the row action strip
// stays the same (DNS/Redirects/Index/Settings/Toggle/Edit/Delete);
// only the hook and the two Refine action buttons change.
import { Button, Card, Space, Table, Tag, Typography } from "antd";
import { GlobalOutlined } from "@ant-design/icons";
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

export type SSLBadge = {
  status: string;
  issuer?: string | null;
  issued_at?: string | null;
  expires_at?: string | null;
};

const renderSSL = (ssl: SSLBadge | null | undefined) => {
  if (!ssl) return <Tag>Off</Tag>;
  switch (ssl.status) {
    case "issued":
      return <Tag color="green">{ssl.issuer || "Let's Encrypt"}</Tag>;
    case "self_signed":
      return <Tag color="orange">Self-signed</Tag>;
    case "pending":
    case "issuing":
    case "renewing":
    case "pending_acme_retry":
      return <Tag color="gold">Issuing…</Tag>;
    case "failed":
      return <Tag color="red">Failed</Tag>;
    case "revoked":
      return <Tag color="red">Revoked</Tag>;
    default:
      return <Tag>Off</Tag>;
  }
};

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  ssl_enabled?: boolean;
  ssl?: SSLBadge | null;
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
  created_at: string;
  updated_at: string;
};

export const DomainList = () => {
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
          Domains
        </Typography.Title>
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
            dataIndex="ssl"
            title="SSL"
            render={(ssl: SSLBadge | null | undefined) => renderSSL(ssl)}
          />
          <Table.Column<Domain>
            title="Actions"
            dataIndex="actions"
            render={(_, r) => (
              <Space>
                <Button
                  type="text"
                  icon={<GlobalOutlined />}
                  onClick={() => navigate(`/jabali-admin/domains/${r.id}/dns`)}
                >
                  DNS
                </Button>
                <DomainRedirectsButton domain={r} />
                <DomainIndexButton domain={r} />
                <DomainSettingsButton domain={r} />
                <DomainToggleButton domain={r} />
                <Button
                  type="text"
                  onClick={() =>
                    navigate(`/jabali-admin/domains/edit/${r.id}`)
                  }
                >
                  Edit
                </Button>
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
