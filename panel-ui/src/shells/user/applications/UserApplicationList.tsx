// User-shell Applications page — lists every install (WordPress today,
// DokuWiki/MediaWiki/etc. as M19 lands them). Same SearchableTable
// pattern as databases. Status → badge mapping covers every value the
// reconciler can emit (see models.ApplicationInstall.Status).

import { useState } from "react";
import {
  Button,
  Space,
  Table,
  Tag,
  Typography,
  Popconfirm,
  message,
  Tooltip,
} from "antd";
import {
  PlusSquareOutlined,
  GlobalOutlined,
  LoadingOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  DeleteOutlined,
  CopyOutlined,
  AppstoreOutlined,
} from "@ant-design/icons";
import { useTable } from "@refinedev/antd";
import { useInvalidate, useGetIdentity } from "@refinedev/core";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { apiClient } from "../../../apiClient";
import { InstallApplicationModal } from "./InstallApplicationModal";
import { CloneApplicationModal } from "./CloneApplicationModal";
import { APP_TYPE_LABELS } from "./appLabels";

type ApplicationInstall = {
  id: string;
  // Pre-M19 rows have no app_type column and the model defaults to
  // "wordpress" — we mirror that default here so a stale read still
  // renders a useful label.
  app_type?: string;
  domain_id: string;
  domain_name: string;
  db_id: string;
  admin_username: string;
  admin_email: string;
  locale: string;
  // Empty string = install at docroot. Non-empty = install at
  // domain.com/<subdirectory>/.
  subdirectory: string;
  status:
    | "pending"
    | "installing"
    | "cloning"
    | "deleting"
    | "ready"
    | "failed";
  version: string | null;
  last_error: string;
  created_at: string;
  updated_at: string;
};

type Identity = { email?: string };

const STATUS_META: Record<
  ApplicationInstall["status"],
  { color: string; icon: React.ReactNode; label: string; spinning: boolean }
> = {
  pending:    { color: "default",    icon: <LoadingOutlined spin />,           label: "Pending",    spinning: true  },
  installing: { color: "processing", icon: <LoadingOutlined spin />,           label: "Installing", spinning: true  },
  cloning:    { color: "processing", icon: <LoadingOutlined spin />,           label: "Cloning",    spinning: true  },
  deleting:   { color: "warning",    icon: <LoadingOutlined spin />,           label: "Deleting",   spinning: true  },
  ready:      { color: "success",    icon: <CheckCircleOutlined />,            label: "Ready",      spinning: false },
  failed:     { color: "error",      icon: <ExclamationCircleOutlined />,      label: "Failed",     spinning: false },
};

export const UserApplicationList = () => {
  const { tableProps, tableQuery, setFilters, filters } = useTable<ApplicationInstall>({
    resource: "applications",
    syncWithLocation: true,
    queryOptions: {
      // react-query v4 signature: (data, query) => number | false.
      refetchInterval: (data) => {
        const rows = (data as { data?: ApplicationInstall[] } | undefined)?.data ?? [];
        const hasTransitional = rows.some(
          (r) =>
            r.status === "pending" ||
            r.status === "installing" ||
            r.status === "cloning" ||
            r.status === "deleting",
        );
        return hasTransitional ? 5000 : false;
      },
    },
  });
  const initialSearch = readQValue(filters);
  const [installOpen, setInstallOpen] = useState(false);
  const [cloneOpen, setCloneOpen] = useState(false);
  const [cloningId, setCloningId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const invalidate = useInvalidate();
  const { data: identity } = useGetIdentity<Identity>();

  const handleDelete = async (row: ApplicationInstall) => {
    setDeletingId(row.id);
    try {
      await apiClient.delete(`/applications/${row.id}`);
      message.success(`Deleting ${row.domain_name || row.domain_id}…`);
      invalidate({ resource: "applications", invalidates: ["list"] });
      invalidate({ resource: "databases", invalidates: ["list"] });
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string; detail?: string } }; message?: string })
          ?.response?.data?.detail ??
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error ??
        (err as { message?: string })?.message ??
        "Delete failed";
      message.error(msg);
    } finally {
      setDeletingId(null);
    }
  };

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
          My Applications
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => setInstallOpen(true)}
        >
          Install application
        </Button>
      </Space>

      <InstallApplicationModal
        open={installOpen}
        onClose={() => setInstallOpen(false)}
        onSuccess={() => tableQuery?.refetch?.()}
        defaultAdminEmail={identity?.email}
      />

      <CloneApplicationModal
        open={cloneOpen}
        onClose={() => {
          setCloneOpen(false);
          setCloningId(null);
        }}
        onSuccess={() => tableQuery?.refetch?.()}
        installId={cloningId ?? ""}
      />

      <SearchableTable<ApplicationInstall>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by domain"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<ApplicationInstall>
          dataIndex="app_type"
          title="App"
          render={(appType: string | undefined) => {
            const key = appType || "wordpress";
            const label = APP_TYPE_LABELS[key] ?? key;
            return (
              <Space size={6}>
                <AppstoreOutlined />
                <Typography.Text>{label}</Typography.Text>
              </Space>
            );
          }}
        />
        <Table.Column<ApplicationInstall>
          dataIndex="domain_name"
          title="Domain"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(domainName: string, record) => {
            const label = domainName || record.domain_id;
            const isLink = record.status === "ready" && !!domainName;
            const path = record.subdirectory ? `/${record.subdirectory}/` : "/";
            return (
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <GlobalOutlined />
                {isLink ? (
                  <a
                    href={`https://${domainName}${path}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    style={{ fontWeight: 500 }}
                  >
                    {label}
                  </a>
                ) : (
                  <span style={{ fontWeight: 500 }}>{label}</span>
                )}
              </div>
            );
          }}
        />
        <Table.Column<ApplicationInstall>
          dataIndex="subdirectory"
          title="Folder"
          render={(subdirectory: string) =>
            subdirectory ? (
              <Typography.Text code>/{subdirectory}/</Typography.Text>
            ) : (
              <Typography.Text type="secondary">/</Typography.Text>
            )
          }
        />
        <Table.Column<ApplicationInstall>
          dataIndex="version"
          title="Version"
          render={(version: string | null) => version || "-"}
        />
        <Table.Column<ApplicationInstall>
          dataIndex="status"
          title="Status"
          render={(status: ApplicationInstall["status"], record) => {
            const meta = STATUS_META[status] ?? STATUS_META.pending;
            const tag = (
              <Tag color={meta.color} icon={meta.icon}>
                {meta.label}
              </Tag>
            );
            if (status === "failed" && record.last_error) {
              return <Tooltip title={record.last_error}>{tag}</Tooltip>;
            }
            return tag;
          }}
        />
        <Table.Column<ApplicationInstall>
          dataIndex="admin_email"
          title="Admin email"
        />
        <Table.Column<ApplicationInstall>
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
        <Table.Column<ApplicationInstall>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => {
            const isDeleting =
              deletingId === r.id || r.status === "deleting";
            const canClone = r.status === "ready" && (r.app_type ?? "wordpress") === "wordpress";
            return (
              <Space size="small">
                <Tooltip
                  title={canClone ? "" : "Clone is only available for healthy WordPress installs"}
                >
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    disabled={!canClone}
                    onClick={() => {
                      setCloningId(r.id);
                      setCloneOpen(true);
                    }}
                  >
                    Clone
                  </Button>
                </Tooltip>
                <Popconfirm
                  title="Delete this application?"
                  description="The database and files will be removed. This cannot be undone."
                  okText="Delete"
                  okButtonProps={{ danger: true }}
                  cancelText="Cancel"
                  onConfirm={() => handleDelete(r)}
                  disabled={isDeleting}
                >
                  <Button
                    size="small"
                    type="text"
                    danger
                    icon={<DeleteOutlined />}
                    loading={isDeleting}
                  >
                    Delete
                  </Button>
                </Popconfirm>
              </Space>
            );
          }}
        />
      </SearchableTable>
    </div>
  );
};
