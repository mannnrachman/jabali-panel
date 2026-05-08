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
import { Button, Card, Dropdown, Modal, Space, Table, Tag, Typography, notification } from "antd";
import { useState } from "react";
import { useNavigate } from "react-router";
import type { SorterResult } from "antd/es/table/interface";
import { useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import { columnSearchProps } from "../../../components/columnSearch";
import { RowActionButton } from "../../../components/RowActionButton";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useDeleteMutation } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
import { DomainSettingsButton } from "../../DomainSettingsButton";
import { DomainRedirectsButton } from "../../DomainRedirectsButton";
import { DomainIndexButton } from "../../DomainIndexButton";
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
  email_enabled?: boolean;
  created_at: string;
  updated_at: string;
};

type ActiveModal = { domainId: string; type: "redirects" | "index" | "settings" } | null;

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
              </>
            )}
          />
        </SearchableTableStringQ>
      </Card>
      <UserDomainDrawer open={drawerOpen} onClose={() => setDrawerOpen(false)} />
    </div>
  );
};
