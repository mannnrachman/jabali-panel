import { useTable } from "@refinedev/antd";
import { CreateButton, DeleteButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { Space, Table, Tag, Typography } from "antd";

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

export const DatabaseList = () => {
  const { tableProps, setFilters, filters } = useTable<Database>({
    resource: "databases",
    syncWithLocation: true,
  });
  const initialSearch = readQValue(filters);

  const engineColorMap: Record<string, string> = {
    mariadb: "blue",
    postgres: "green",
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
          Databases
        </Typography.Title>
        <CreateButton />
      </Space>

      <SearchableTable<Database>
        {...tableProps}
        rowKey="id"
        initialSearch={initialSearch}
        searchPlaceholder="Search by database name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<Database>
          dataIndex="name"
          title="Database"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
        />
        <Table.Column<Database>
          dataIndex="user_id"
          title="User ID"
          render={(value: string) => value.substring(0, 8)}
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
