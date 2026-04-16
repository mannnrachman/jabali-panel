import { useTable } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";
import { CreateButton, DeleteButton, EditButton } from "@refinedev/antd";

type Package = {
  id: string;
  name: string;
  disk_quota_mb: number;
  bandwidth_quota_mb: number;
  max_domains: number;
  max_email_accounts: number;
  max_databases: number;
  max_ftp_accounts: number;
  ssh_enabled: boolean;
  cgi_enabled: boolean;
  created_at: string;
  updated_at: string;
};

export const PackageList = () => {
  const { tableProps } = useTable<Package>({
    resource: "packages",
    syncWithLocation: true,
  });

  // Format quota as "∞" if 0, otherwise the number
  const formatQuota = (value: number) => (value === 0 ? "∞" : value);

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
          Packages
        </Typography.Title>
        <CreateButton />
      </Space>

      <Table<Package> {...tableProps} rowKey="id" bordered>
        <Table.Column<Package> dataIndex="name" title="Name" />
        <Table.Column<Package>
          dataIndex="disk_quota_mb"
          title="Disk (MB)"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column<Package>
          dataIndex="bandwidth_quota_mb"
          title="Bandwidth (MB)"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column<Package>
          dataIndex="max_domains"
          title="Domains"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column<Package>
          dataIndex="max_email_accounts"
          title="Email"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column<Package>
          dataIndex="max_databases"
          title="DB"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column<Package>
          dataIndex="ssh_enabled"
          title="SSH"
          render={(enabled: boolean) =>
            enabled ? <Tag color="green">yes</Tag> : <Tag>no</Tag>
          }
        />
        <Table.Column<Package>
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
