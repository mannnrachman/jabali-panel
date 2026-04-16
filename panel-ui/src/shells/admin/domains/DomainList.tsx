import { useTable } from "@refinedev/antd";
import { DeleteButton, EditButton } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  created_at: string;
  updated_at: string;
};

export const DomainList = () => {
  const { tableProps } = useTable<Domain>({
    resource: "domains",
    syncWithLocation: true,
  });

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
          Domains
        </Typography.Title>
      </Space>

      <Table<Domain> {...tableProps} rowKey="id" bordered>
        <Table.Column<Domain> dataIndex="name" title="Name" />
        <Table.Column<Domain>
          dataIndex="user_id"
          title="User ID"
          render={(value: string) => value.substring(0, 8)}
        />
        <Table.Column<Domain> dataIndex="doc_root" title="Doc Root" />
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
          title="Actions"
          dataIndex="actions"
          render={(_, r) => (
            <Space>
              <EditButton hideText size="small" recordItemId={r.id} />
              <DeleteButton hideText size="small" recordItemId={r.id} />
            </Space>
          )}
        />
      </Table>
    </div>
  );
};
