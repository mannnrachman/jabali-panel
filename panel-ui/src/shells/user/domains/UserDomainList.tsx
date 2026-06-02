// UserDomainList — tenant view of domains they own. Same row-action
// strip as the admin list (DNS/Redirects/Index/Settings/Toggle/Delete)
// minus the Edit button (users edit per-domain config via the row
// buttons rather than a full edit page).
import {
  PlusSquareOutlined,
  GlobalOutlined,
  MoreOutlined,
  SwapOutlined,
  FileTextOutlined,
  SettingOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  DeleteOutlined,
} from "@icons";
import { ApiOutlined } from "@ant-design/icons";
import { Button, Card, Dropdown, Modal, Space, Table, Tag, Typography, notification } from "antd";
import { useState } from "react";
import { useNavigate } from "react-router";
import type { SorterResult } from "antd/es/table/interface";
import { useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import { columnSearchProps } from "../../../components/columnSearch";
import { RowActionButton } from "../../../components/RowActionButton";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { humanBytes } from "../../../utils/bytes";
import { useDeleteMutation } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
import { DomainSettingsButton } from "../../DomainSettingsButton";
import { DomainRedirectsButton } from "../../DomainRedirectsButton";
import { DomainIndexButton } from "../../DomainIndexButton";
import { DomainRuntimeSettingsModal } from "../../DomainRuntimeSettingsModal";
import { UserDomainDrawer } from "./UserDomainDrawer";

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
      <span>{name}</span>
    </div>
    <Typography.Text type="secondary">{stripHomePrefix(docRoot)}</Typography.Text>
  </div>
);

// SSL state values come from panel-api/internal/repository/
// domain_repository.go computeSSLState: "active_le" (valid LE
// cert), "self_signed", "pending", "issuing", "renewing",
// "pending_acme_retry", "failed", "revoked", "off".
//
// Mirrors admin DomainList renderSSL (DomainList.tsx 66-80) so
// user + admin shells render identically.
const getSSLTagColor = (state?: string): string => {
  switch (state) {
    case "active_le":
      return "gold"; // Let's Encrypt rendered yellow per operator request
    case "active":
      return "green";
    case "self_signed":
      return "orange";
    case "pending":
    case "issuing":
    case "renewing":
    case "pending_acme_retry":
      return "green";
    case "failed":
    case "error":
    case "revoked":
      return "red";
    default:
      return "default";
  }
};

const getSSLTagLabel = (state?: string): string => {
  switch (state) {
    case "active_le":
      return "Let's Encrypt";
    case "active":
      return "Active";
    case "self_signed":
      return "Self-signed";
    case "pending":
    case "issuing":
    case "renewing":
    case "pending_acme_retry":
      return "Issuing…";
    case "failed":
    case "error":
      return "Failed";
    case "revoked":
      return "Revoked";
    case "":
    case undefined:
      return "Off";
    default:
      return state;
  }
};

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  runtime_type?: string;
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
  email_enabled?: boolean;
  bytes_30d?: number;
  created_at: string;
  updated_at: string;
};

type ActiveModal = { domainId: string; type: "redirects" | "index" | "settings" | "runtime" } | null;

export const UserDomainList = () => {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [activeModal, setActiveModal] = useState<ActiveModal>(null);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
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
          flexWrap: "wrap",
          rowGap: 8,
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          <GlobalOutlined /> Domains
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => setDrawerOpen(true)}
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
            {...columnSearchProps<Domain>({
              placeholder: "Search by domain name",
              currentQ: query.params.q,
              onSearch: (v) => query.setParams({ q: v, page: 1 }),
            })}
            render={(name: string, record: Domain) =>
              renderDomainCell(name, record.doc_root)
            }
          />
          <Table.Column<Domain>
            dataIndex="runtime_type"
            title="Runtime"
            render={(type?: string) => {
              const rt = type || "php";
              let color = "purple";
              let label = "PHP";
              if (rt === "nodejs") { color = "green"; label = "Node.js"; }
              else if (rt === "python") { color = "blue"; label = "Python"; }
              else if (rt === "go") { color = "cyan"; label = "Go"; }
              else if (rt === "docker") { color = "geekblue"; label = "Docker"; }
              else if (rt === "static") { color = "orange"; label = "Static"; }
              return <Tag color={color} style={{ borderRadius: 4, textTransform: "capitalize" }}>{label}</Tag>;
            }}
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
            dataIndex="bytes_30d"
            title="BW (30d)"
            render={(v: number | undefined) => humanBytes(v ?? 0)}
          />
          <Table.Column<Domain>
            title="Actions"
            dataIndex="actions"
            render={(_, r) => (
              <>
                <Space>
                <RowActionButton
                  icon={<GlobalOutlined />}
                  onClick={() => navigate(`/jabali-panel/domains/${r.id}/dns`)}
                >
                  DNS
                </RowActionButton>
                <Dropdown
                  trigger={["click"]}
                  menu={{
                    items: [
                      {
                        key: "redirects",
                        label: "Redirects",
                        icon: <SwapOutlined />,
                        onClick: () => setActiveModal({ domainId: r.id, type: "redirects" }),
                      },
                      {
                        key: "index",
                        label: "Index",
                        icon: <FileTextOutlined />,
                        onClick: () => setActiveModal({ domainId: r.id, type: "index" }),
                      },
                      {
                        key: "settings",
                        label: "Nginx Directives",
                        icon: <SettingOutlined />,
                        onClick: () => setActiveModal({ domainId: r.id, type: "settings" }),
                      },
                      {
                        key: "runtime",
                        label: "Runtime & Environment",
                        icon: <ApiOutlined />,
                        onClick: () => setActiveModal({ domainId: r.id, type: "runtime" }),
                      },
                      {
                        key: "toggle",
                        label: r.is_enabled ? "Disable" : "Enable",
                        icon: r.is_enabled ? <PauseCircleOutlined /> : <PlayCircleOutlined />,
                        disabled: togglingId === r.id,
                        onClick: async () => {
                          setTogglingId(r.id);
                          try {
                            await apiClient.patch(`/domains/${r.id}`, {
                              is_enabled: !r.is_enabled,
                            });
                            notification.success({
                              message: r.is_enabled ? "Domain disabled" : "Domain enabled",
                            });
                            qc.invalidateQueries({ queryKey: ["list", "domains"] });
                            qc.invalidateQueries({ queryKey: ["one", "domains", r.id] });
                          } catch (err) {
                            notification.error({
                              message: "Failed to toggle",
                              description: (err as Error).message,
                            });
                          } finally {
                            setTogglingId(null);
                          }
                        },
                      },
                      { type: "divider" },
                      {
                        key: "delete",
                        label: "Delete",
                        icon: <DeleteOutlined />,
                        danger: true,
                        onClick: () =>
                          Modal.confirm({
                            title: `Delete domain "${r.name}"?`,
                            content: "This cannot be undone.",
                            okText: "Delete",
                            okType: "danger",
                            onOk: async () => {
                              await deleteMutation.mutateAsync({ id: r.id });
                            },
                          }),
                      },
                    ],
                  }}
                >
                  <RowActionButton icon={<MoreOutlined />} color="default">
                    More
                  </RowActionButton>
                </Dropdown>
                </Space>
                {activeModal?.domainId === r.id && activeModal.type === "redirects" && (
                  <DomainRedirectsButton
                    domain={r}
                    open={true}
                    onClose={() => setActiveModal(null)}
                  />
                )}
                {activeModal?.domainId === r.id && activeModal.type === "index" && (
                  <DomainIndexButton
                    domain={r}
                    open={true}
                    onClose={() => setActiveModal(null)}
                  />
                )}
                {activeModal?.domainId === r.id && activeModal.type === "settings" && (
                  <DomainSettingsButton
                    domain={r}
                    open={true}
                    onClose={() => setActiveModal(null)}
                  />
                )}
                {activeModal?.domainId === r.id && activeModal.type === "runtime" && (
                  <DomainRuntimeSettingsModal
                    domain={r}
                    open={true}
                    onClose={() => setActiveModal(null)}
                    onSuccess={() => qc.invalidateQueries({ queryKey: ["list", "domains"] })}
                  />
                )}
              </>
            )}
          />
        </SearchableTableStringQ>
      </Card>
      <UserDomainDrawer open={drawerOpen} onClose={() => setDrawerOpen(false)} />
    </div>
  );
};
