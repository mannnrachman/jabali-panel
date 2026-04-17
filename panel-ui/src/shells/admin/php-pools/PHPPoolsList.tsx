import { useTable } from "@refinedev/antd";
import { DeleteButton, EditButton } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";

export type PHPPool = {
  id: string;
  user_id: string;
  php_version: string;
  pm_mode: string;
  pm_max_children: number;
  process_idle_timeout_seconds: number;
  status: string;
  last_error?: string;
  created_at: string;
};

export const PHPPoolsList = () => {
  const { tableProps } = useTable<PHPPool>({
    resource: "php-pools",
    syncWithLocation: true,
  });

  const statusColorMap: Record<string, string> = {
    active: "green",
    error: "red",
    pending: "blue",
    stopped: "default",
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
          PHP-FPM Pools
        </Typography.Title>
      </Space>

      <Table<PHPPool>
        {...tableProps}
        rowKey="id"
        bordered
      >
        <Table.Column<PHPPool>
          dataIndex="user_id"
          title="User"
          render={(value: string) => value.substring(0, 8)}
        />
        <Table.Column<PHPPool>
          dataIndex="php_version"
          title="PHP Version"
          render={(version: string) => (
            <Tag color="blue">{version}</Tag>
          )}
        />
        <Table.Column<PHPPool>
          dataIndex="pm_mode"
          title="Process Mode"
          render={(mode: string) => (
            <Tag color="cyan">{mode}</Tag>
          )}
        />
        <Table.Column<PHPPool>
          dataIndex="pm_max_children"
          title="Max Children"
        />
        <Table.Column<PHPPool>
          dataIndex="status"
          title="Status"
          render={(status: string) => (
            <Tag color={statusColorMap[status] || "default"}>{status}</Tag>
          )}
        />
        <Table.Column<PHPPool>
          dataIndex="last_error"
          title="Last Error"
          render={(error?: string) => (
            <div style={{ maxWidth: 200, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {error || "-"}
            </div>
          )}
        />
        <Table.Column<PHPPool>
          dataIndex="created_at"
          title="Created"
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
        <Table.Column<PHPPool>
          title="Actions"
          fixed="right"
          width={120}
          render={(_, record) => (
            <Space size="small">
              <EditButton hideText size="small" type="text" recordItemId={record.id} />
              <DeleteButton hideText size="small" type="text" recordItemId={record.id} />
            </Space>
          )}
        />
      </Table>
    </div>
  );
};
