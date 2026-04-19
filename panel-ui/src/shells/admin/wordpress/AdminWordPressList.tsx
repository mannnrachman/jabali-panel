// Admin-shell cross-user WordPress installs list — read-only view of all
// WordPress sites across all users. Admins act via impersonation if they
// need to modify any install.
//
// Status badge styling is inlined from UserWordPressList to keep
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
} from "@ant-design/icons";
import { useTable } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";

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

export const AdminWordPressList = () => {
  const { tableProps, setFilters, filters } = useTable<WordPressInstall>({
    resource: "wordpress-installs",
    syncWithLocation: true,
    queryOptions: {
      // react-query v4: (data, query) => number | false. data is
      // Refine's list envelope, not the Query object.
      refetchInterval: (data) => {
        const rows = (data as { data?: WordPressInstall[] } | undefined)?.data ?? [];
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
          WordPress Installs (All Users)
        </Typography.Title>
      </Space>

      <SearchableTable<WordPressInstall>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by domain or user"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<WordPressInstall>
          dataIndex="domain_name"
          title="Domain"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(domainName: string, record) => {
            const label = domainName || record.domain_id;
            const isLink = record.status === "ready" && !!domainName;
            return (
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <GlobalOutlined />
                {isLink ? (
                  <a
                    href={`https://${domainName}/`}
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
        <Table.Column<WordPressInstall>
          dataIndex="admin_email"
          title="User"
          render={(email: string) => {
            return <span>{email}</span>;
          }}
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
