import { useTable } from "@refinedev/antd";
import { DeleteButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { Button, Space, Table, Tag, Typography } from "antd";
import { DatabaseOutlined, PlusSquareOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router";

export type Database = {
  id: string;
  user_id: string;
  name: string;
  engine: "mariadb" | "postgres";
  charset?: string;
  collation?: string;
  created_at: string;
  updated_at: string;
};

export const UserDatabaseList = () => {
  const navigate = useNavigate();
  const { tableProps, setFilters, filters } = useTable<Database>({
    resource: "databases",
    syncWithLocation: true,
  });
  const initialSearch = readQValue(filters);

  const engineColorMap: Record<string, string> = {
    mariadb: "blue",
    postgres: "green",
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
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => navigate("/jabali-panel/databases/create")}
        >
          Create Database
        </Button>
      </Space>

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
          render={(_, r) => (
            <Space size="small">
              <DeleteButton hideText size="small" type="text" resource="databases" recordItemId={r.id} />
            </Space>
          )}
        />
      </SearchableTable>
    </div>
  );
};
