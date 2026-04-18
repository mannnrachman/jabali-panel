// User-shell WordPress page — list of the caller's installs with
// install + delete actions. Matches the Databases page pattern:
// SearchableTable, useTable against the resource's API slug, and
// a modal for the create/install flow.
//
// Status → badge color mapping covers every value wordPressInstallRepo
// can emit (see models.WordPressInstall.Status). Transitional states
// (pending, installing, cloning, deleting) show a spinner so the user
// knows the background agent is still working; terminal states (ready,
// failed) show green/red.

import { useMemo, useState } from "react";
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
} from "@ant-design/icons";
import { useTable } from "@refinedev/antd";
import { useInvalidate, useGetIdentity } from "@refinedev/core";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { apiClient } from "../../../apiClient";
import { InstallWordPressModal } from "./InstallWordPressModal";
import { CloneWordPressModal } from "./CloneWordPressModal";

type WordPressInstall = {
  id: string;
  domain_id: string;
  domain_name: string;
  db_id: string;
  admin_username: string;
  admin_email: string;
  locale: string;
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
  WordPressInstall["status"],
  { color: string; icon: React.ReactNode; label: string; spinning: boolean }
> = {
  pending:    { color: "default",    icon: <LoadingOutlined spin />,           label: "Pending",    spinning: true  },
  installing: { color: "processing", icon: <LoadingOutlined spin />,           label: "Installing", spinning: true  },
  cloning:    { color: "processing", icon: <LoadingOutlined spin />,           label: "Cloning",    spinning: true  },
  deleting:   { color: "warning",    icon: <LoadingOutlined spin />,           label: "Deleting",   spinning: true  },
  ready:      { color: "success",    icon: <CheckCircleOutlined />,            label: "Ready",      spinning: false },
  failed:     { color: "error",      icon: <ExclamationCircleOutlined />,      label: "Failed",     spinning: false },
};

export const UserWordPressList = () => {
  const { tableProps, tableQuery, setFilters, filters } = useTable<WordPressInstall>({
    resource: "wordpress-installs",
    syncWithLocation: true,
    // Poll while any row is in a transitional state, so the user sees
    // installing→ready without manual refresh. Refine's useTable lets
    // us pass refetchInterval through to react-query.
    queryOptions: {
      refetchInterval: (query) => {
        if (!query) return false;
        const data = query.state.data as { data?: WordPressInstall[] } | undefined;
        const rows = data?.data ?? [];
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

  // Domains already hosting an install — fed into the modal so the
  // domain dropdown doesn't offer them (the backend would 409).
  const alreadyHostedDomainIds = useMemo(() => {
    const ids = new Set<string>();
    const rows = (tableProps.dataSource ?? []) as WordPressInstall[];
    for (const r of rows) {
      // Don't block the domain just because a prior install is being
      // deleted — but keep it out of the dropdown while the delete is
      // in flight so users don't double-create.
      ids.add(r.domain_id);
    }
    return ids;
  }, [tableProps.dataSource]);

  const handleDelete = async (row: WordPressInstall) => {
    setDeletingId(row.id);
    try {
      await apiClient.delete(`/wordpress-installs/${row.id}`);
      message.success(`Deleting ${row.domain_name || row.domain_id}…`);
      invalidate({ resource: "wordpress-installs", invalidates: ["list"] });
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
          My WordPress Sites
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => setInstallOpen(true)}
        >
          New WordPress install
        </Button>
      </Space>

      <InstallWordPressModal
        open={installOpen}
        onClose={() => setInstallOpen(false)}
        onSuccess={() => tableQuery?.refetch?.()}
        alreadyHostedDomainIds={alreadyHostedDomainIds}
        defaultAdminEmail={identity?.email}
      />

      <CloneWordPressModal
        open={cloneOpen}
        onClose={() => {
          setCloneOpen(false);
          setCloningId(null);
        }}
        onSuccess={() => tableQuery?.refetch?.()}
        installId={cloningId ?? ""}
        alreadyHostedDomainIds={alreadyHostedDomainIds}
      />

      <SearchableTable<WordPressInstall>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by domain"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<WordPressInstall>
          dataIndex="domain_name"
          title="Domain"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(domainName: string, record) => (
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <GlobalOutlined />
              <span style={{ fontWeight: 500 }}>
                {domainName || record.domain_id}
              </span>
              {record.status === "ready" && domainName && (
                <a
                  href={`https://${domainName}/`}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{ fontSize: 12 }}
                >
                  open ↗
                </a>
              )}
            </div>
          )}
        />
        <Table.Column<WordPressInstall>
          dataIndex="version"
          title="Version"
          render={(version: string | null) => version || "-"}
        />
        <Table.Column<WordPressInstall>
          dataIndex="status"
          title="Status"
          render={(status: WordPressInstall["status"], record) => {
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
        <Table.Column<WordPressInstall>
          dataIndex="admin_email"
          title="Admin email"
        />
        <Table.Column<WordPressInstall>
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
        <Table.Column<WordPressInstall>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => {
            const isDeleting =
              deletingId === r.id || r.status === "deleting";
            const canClone = r.status === "ready";
            return (
              <Space size="small">
                <Tooltip
                  title={canClone ? "" : "Clone is only available for healthy installs"}
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
                  title="Delete this WordPress install?"
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
