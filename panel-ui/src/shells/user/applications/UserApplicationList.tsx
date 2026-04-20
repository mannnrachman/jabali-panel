// User-shell Applications page — lists every install (WordPress
// today, DokuWiki/MediaWiki/etc. as M19 lands them). Post-M21:
// useTableURL with a custom useQuery `refetchInterval` so
// transitional statuses (pending/installing/cloning/deleting) poll
// until ready.
import { useEffect, useState } from "react";
import {
  Button,
  Card,
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
} from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import type { SorterResult } from "antd/es/table/interface";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { apiClient } from "../../../apiClient";
import { useAuth } from "../../../auth/AuthContext";
import { useTableURL } from "../../../hooks/useTableURL";
import { InstallApplicationModal } from "./InstallApplicationModal";
import { CloneApplicationModal } from "./CloneApplicationModal";
import { APP_TYPE_LABELS } from "./appLabels";
import { CmsIcon } from "./CmsIcon";

type ApplicationInstall = {
  id: string;
  app_type?: string;
  domain_id: string;
  domain_name: string;
  db_id: string;
  admin_username: string;
  admin_email: string;
  locale: string;
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

const STATUS_META: Record<
  ApplicationInstall["status"],
  { color: string; icon: React.ReactNode; label: string; spinning: boolean }
> = {
  pending:    { color: "default",    icon: <LoadingOutlined spin />,      label: "Pending",    spinning: true  },
  installing: { color: "processing", icon: <LoadingOutlined spin />,      label: "Installing", spinning: true  },
  cloning:    { color: "processing", icon: <LoadingOutlined spin />,      label: "Cloning",    spinning: true  },
  deleting:   { color: "warning",    icon: <LoadingOutlined spin />,      label: "Deleting",   spinning: true  },
  ready:      { color: "success",    icon: <CheckCircleOutlined />,       label: "Ready",      spinning: false },
  failed:     { color: "error",      icon: <ExclamationCircleOutlined />, label: "Failed",     spinning: false },
};

const TRANSITIONAL = new Set<ApplicationInstall["status"]>([
  "pending",
  "installing",
  "cloning",
  "deleting",
]);

export const UserApplicationList = () => {
  const { user } = useAuth();
  const qc = useQueryClient();

  const tableQuery = useTableURL<ApplicationInstall>({
    resource: "applications",
    defaultSort: "domain_name",
    defaultOrder: "asc",
  });

  const [installOpen, setInstallOpen] = useState(false);
  const [cloneOpen, setCloneOpen] = useState(false);
  const [cloningId, setCloningId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // Poll the list while any row is transitional (pending/installing/
  // cloning/deleting). Five-second cadence matches what Refine's old
  // refetchInterval returned. refetch identity is stable, so only
  // `active` triggers re-installing the timer.
  const hasTransitional = tableQuery.items.some((r) =>
    TRANSITIONAL.has(r.status),
  );
  useEffect(() => {
    if (!hasTransitional) return;
    const h = setInterval(() => {
      tableQuery.refetch();
    }, 5000);
    return () => clearInterval(h);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasTransitional]);

  const handleDelete = async (row: ApplicationInstall) => {
    setDeletingId(row.id);
    try {
      await apiClient.delete(`/applications/${row.id}`);
      message.success(`Deleting ${row.domain_name || row.domain_id}…`);
      qc.invalidateQueries({ queryKey: ["list", "applications"] });
      qc.invalidateQueries({ queryKey: ["list", "databases"] });
    } catch (err) {
      const msg =
        (err as {
          response?: { data?: { error?: string; detail?: string } };
          message?: string;
        })?.response?.data?.detail ??
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ??
        (err as { message?: string })?.message ??
        "Delete failed";
      message.error(msg);
    } finally {
      setDeletingId(null);
    }
  };

  const handleTableChange: React.ComponentProps<
    typeof Table<ApplicationInstall>
  >["onChange"] = (pagination, _filters, sorter) => {
    const single = Array.isArray(sorter)
      ? (sorter[0] as SorterResult<ApplicationInstall> | undefined)
      : (sorter as SorterResult<ApplicationInstall>);
    tableQuery.setParams({
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
        onSuccess={() => tableQuery.refetch()}
        defaultAdminEmail={user?.email}
      />

      <CloneApplicationModal
        open={cloneOpen}
        onClose={() => {
          setCloneOpen(false);
          setCloningId(null);
        }}
        onSuccess={() => tableQuery.refetch()}
        installId={cloningId ?? ""}
      />

      <Card>
        <SearchableTableStringQ<ApplicationInstall>
          rowKey="id"
          loading={tableQuery.isLoading}
          dataSource={tableQuery.items}
          initialSearch={tableQuery.params.q}
          searchPlaceholder="Search by domain"
          onSearchChange={(q) => tableQuery.setParams({ q, page: 1 })}
          pagination={{
            current: tableQuery.params.page,
            pageSize: tableQuery.params.pageSize,
            total: tableQuery.total,
          }}
          onChange={handleTableChange}
        >
          <Table.Column<ApplicationInstall>
            dataIndex="app_type"
            title="App"
            render={(appType: string | undefined) => {
              const key = appType || "wordpress";
              const label = APP_TYPE_LABELS[key] ?? key;
              return (
                <Space size={6}>
                  <CmsIcon appType={key} />
                  <Typography.Text>{label}</Typography.Text>
                </Space>
              );
            }}
          />
          <Table.Column<ApplicationInstall>
            dataIndex="domain_name"
            title="Domain"
            key="domain_name"
            sorter={{ multiple: 1 }}
            defaultSortOrder="ascend"
            render={(domainName: string, record) => {
              const base = domainName || record.domain_id;
              const path = record.subdirectory
                ? `/${record.subdirectory}/`
                : "/";
              const label = `${base}${path}`;
              const isLink = record.status === "ready" && !!domainName;
              return (
                <div
                  style={{ display: "flex", alignItems: "center", gap: 8 }}
                >
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
            key="created_at"
            sorter={{ multiple: 2 }}
            render={(date: string) => new Date(date).toLocaleDateString()}
          />
          <Table.Column<ApplicationInstall>
            title="Actions"
            dataIndex="actions"
            render={(_, r) => {
              const isDeleting =
                deletingId === r.id || r.status === "deleting";
              const canClone =
                r.status === "ready" &&
                (r.app_type ?? "wordpress") === "wordpress";
              return (
                <Space size="small">
                  <Tooltip
                    title={
                      canClone
                        ? ""
                        : "Clone is only available for healthy WordPress installs"
                    }
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
        </SearchableTableStringQ>
      </Card>
    </div>
  );
};

