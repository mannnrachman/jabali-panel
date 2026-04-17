import { useTable } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";
import { CreateButton, DeleteButton, EditButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";

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
  const { tableProps, setFilters } = useTable<Package>({
    resource: "packages",
    syncWithLocation: true,
  });

  // "∞" for 0 quotas keeps the cell readable instead of printing 0.
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

      <SearchableTable<Package>
        {...tableProps}
        rowKey="id"
        bordered
        searchPlaceholder="Search by package name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column
          dataIndex="name"
          title="Name"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
        />
        <Table.Column
          dataIndex="disk_quota_mb"
          title="Disk (MB)"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column
          dataIndex="bandwidth_quota_mb"
          title="Bandwidth (MB)"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column
          dataIndex="max_domains"
          title="Domains"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column
          dataIndex="max_email_accounts"
          title="Email"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column
          dataIndex="max_databases"
          title="DB"
          render={(value: number) => formatQuota(value)}
        />
        <Table.Column
          dataIndex="ssh_enabled"
          title="SSH"
          render={(enabled: boolean) =>
            enabled ? <Tag color="green">yes</Tag> : <Tag>no</Tag>
          }
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 1 }}
        />
        <Table.Column
          title="Actions"
          dataIndex="actions"
          render={(_: unknown, r: Package) => (
            <Space>
              <EditButton hideText size="small" recordItemId={r.id} />
              <DeleteButton hideText size="small" recordItemId={r.id} />
            </Space>
          )}
        />
      </SearchableTable>
    </div>
  );
};
