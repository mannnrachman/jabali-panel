// Admin-shell cross-user applications list — read-only view of every
// installed application (WordPress today, more as M19 lands them).
// Admins act via impersonation if they need to modify any install.
//
// Status badge styling is inlined from UserApplicationList to keep
// coupling low and avoid tight dependency on that component.

import {
  Space,
  Table,
  Tag,
  Typography,
  Tooltip,
} from "antd";
import {
  GlobalOutlined,
  LoadingOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  AppstoreOutlined,
} from "@ant-design/icons";
import { useTable } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { APP_TYPE_LABELS } from "../../user/applications/appLabels";

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
  pending:    { color: "default",    icon: <LoadingOutlined spin />,           label: "Pending",    spinning: true  },
  installing: { color: "processing", icon: <LoadingOutlined spin />,           label: "Installing", spinning: true  },
  cloning:    { color: "processing", icon: <LoadingOutlined spin />,           label: "Cloning",    spinning: true  },
  deleting:   { color: "warning",    icon: <LoadingOutlined spin />,           label: "Deleting",   spinning: true  },
  ready:      { color: "success",    icon: <CheckCircleOutlined />,            label: "Ready",      spinning: false },
  failed:     { color: "error",      icon: <ExclamationCircleOutlined />,      label: "Failed",     spinning: false },
};

export const AdminApplicationList = () => {
  const { tableProps, setFilters, filters } = useTable<ApplicationInstall>({
    resource: "applications",
    syncWithLocation: true,
    queryOptions: {
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
          Applications (All Users)
        </Typography.Title>
      </Space>

      <SearchableTable<ApplicationInstall>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by domain or user"
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
            const base = domainName || record.domain_id;
            const path = record.subdirectory ? `/${record.subdirectory}/` : "/";
            const label = `${base}${path}`;
            const isLink = record.status === "ready" && !!domainName;
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
          dataIndex="admin_email"
          title="User"
          render={(email: string) => {
            return <span>{email}</span>;
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
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          defaultSortOrder="descend"
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
      </SearchableTable>
    </div>
  );
};
