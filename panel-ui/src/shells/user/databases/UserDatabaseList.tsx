import { useTable } from "@refinedev/antd";
import { DeleteButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { Button, Space, Table, Tag, Typography, message, Tooltip } from "antd";
import { DatabaseOutlined, PlusSquareOutlined, LinkOutlined, ThunderboltOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router";
import { ssoPhpMyAdmin } from "../../../apiClient";
import { useState } from "react";
import { QuickSetupModal } from "./QuickSetupModal";

export type Database = {
  id: string;
  user_id: string;
  name: string;
  engine: "mariadb" | "postgres";
  charset?: string;
  collation?: string;
  created_at: string;
  updated_at: string;
  size_bytes?: number;
};

export const UserDatabaseList = () => {
  const navigate = useNavigate();
  const { tableProps, tableQuery, setFilters, filters } = useTable<Database>({
    resource: "databases",
    syncWithLocation: true,
  });
  const initialSearch = readQValue(filters);
  const [loadingPhpMyAdminId, setLoadingPhpMyAdminId] = useState<string | null>(null);
  const [quickSetupOpen, setQuickSetupOpen] = useState(false);

  const engineColorMap: Record<string, string> = {
    mariadb: "blue",
    postgres: "green",
  };

  const formatBytes = (bytes: number | undefined): string => {
    if (bytes === undefined || bytes === 0) return "0 B";

    const units = ["B", "KB", "MB", "GB", "TB"];
    let size = bytes;
    let unitIndex = 0;

    while (size >= 1024 && unitIndex < units.length - 1) {
      size /= 1024;
      unitIndex++;
    }

    if (unitIndex === 0) {
      return `${Math.floor(size)} B`;
    }
    return `${size.toFixed(1)} ${units[unitIndex]}`;
  };

  const renderDatabaseCell = (name: string, engine: string) => (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
        <DatabaseOutlined />
        <span style={{ fontWeight: 500 }}>{name}</span>
      </div>
      <div style={{ color: "#999", fontSize: "12px" }}>{engine}</div>
    </div>
  );

  const handleOpenPhpMyAdmin = async (row: Database) => {
    try {
      setLoadingPhpMyAdminId(row.id);
      const response = await ssoPhpMyAdmin(row.id);
      window.location.assign(response.redirect_url);
    } catch (error) {
      const errorMsg =
        error instanceof Error ? error.message : "Could not open phpMyAdmin";
      message.error(`Could not open phpMyAdmin: ${errorMsg}`);
    } finally {
      setLoadingPhpMyAdminId(null);
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
          My Databases
        </Typography.Title>
        <Space>
          <Button
            icon={<ThunderboltOutlined />}
            onClick={() => setQuickSetupOpen(true)}
          >
            Quick Setup
          </Button>
          <Button
            type="primary"
            icon={<PlusSquareOutlined />}
            onClick={() => navigate("/jabali-panel/databases/create")}
          >
            Create Database
          </Button>
        </Space>
      </Space>

      <QuickSetupModal
        open={quickSetupOpen}
        onClose={() => setQuickSetupOpen(false)}
        onSuccess={() => tableQuery?.refetch?.()}
      />

      <SearchableTable<Database>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by database name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<Database>
          dataIndex="name"
          title="Database"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(name: string, record: Database) => renderDatabaseCell(name, record.engine)}
        />
        <Table.Column<Database>
          dataIndex="engine"
          title="Engine"
          render={(engine: string) => (
            <Tag color={engineColorMap[engine] || "default"}>{engine}</Tag>
          )}
        />
        <Table.Column<Database>
          dataIndex="size_bytes"
          title="Size"
          sorter={{ multiple: 3 }}
          render={(size_bytes?: number) => formatBytes(size_bytes)}
        />
        <Table.Column<Database>
          dataIndex="charset"
          title="Charset"
          render={(charset?: string) => charset || "-"}
        />
        <Table.Column<Database>
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
        <Table.Column<Database>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => {
            const isPostgres = r.engine === "postgres";
            const isLoading = loadingPhpMyAdminId === r.id;

            return (
              <Space size="small">
                <Tooltip title={isPostgres ? "phpMyAdmin supports MySQL/MariaDB only" : ""}>
                  <Button
                    type="link"
                    size="small"
                    icon={<LinkOutlined />}
                    onClick={() => handleOpenPhpMyAdmin(r)}
                    disabled={isPostgres || isLoading}
                    loading={isLoading}
                  >
                    Open in phpMyAdmin
                  </Button>
                </Tooltip>
                <DeleteButton hideText size="small" type="text" resource="databases" recordItemId={r.id} />
              </Space>
            );
          }}
        />
      </SearchableTable>
    </div>
  );
};
