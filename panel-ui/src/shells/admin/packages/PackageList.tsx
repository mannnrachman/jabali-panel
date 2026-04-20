// PackageList — hosting packages admin list. Sortable + searchable.
// Post-M21: useTable → useTableURL, <CreateButton>/<EditButton>/
// <DeleteButton> replaced with plain react-router <Button>s + a
// RowDeleteButton wired to useDeleteMutation.
import { Button, Card, Space, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";
import type { SorterResult } from "antd/es/table/interface";

import { RowDeleteButton } from "../../../components/RowDeleteButton";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useDeleteMutation } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";

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

// "∞" for 0 quotas keeps the cell readable instead of printing 0.
const formatQuota = (value: number) => (value === 0 ? "∞" : value);

export const PackageList = () => {
  const navigate = useNavigate();
  const query = useTableURL<Package>({
    resource: "packages",
    defaultSort: "name",
    defaultOrder: "asc",
  });
  const deleteMutation = useDeleteMutation({ resource: "packages" });

  const handleTableChange: React.ComponentProps<typeof Table<Package>>["onChange"] = (
    pagination,
    _filters,
    sorter,
  ) => {
    const single = Array.isArray(sorter)
      ? (sorter[0] as SorterResult<Package> | undefined)
      : (sorter as SorterResult<Package>);
    query.setParams({
      page: pagination.current ?? 1,
      pageSize: pagination.pageSize ?? 20,
      sort: single?.columnKey ? String(single.columnKey) : undefined,
      order:
        single?.order === "ascend"
          ? "asc"
          : single?.order === "descend"
            ? "desc"
            : undefined,
    });
  };

  return (
    <div>
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
        <Button
          type="primary"
          onClick={() => navigate("/jabali-admin/packages/create")}
        >
          Create
        </Button>
      </Space>

      <Card>
        <SearchableTableStringQ<Package>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search by package name"
          onSearchChange={(q) => query.setParams({ q, page: 1 })}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
          }}
          onChange={handleTableChange}
        >
          <Table.Column
            dataIndex="name"
            title="Name"
            key="name"
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
            key="created_at"
            sorter={{ multiple: 1 }}
          />
          <Table.Column
            title="Actions"
            dataIndex="actions"
            render={(_: unknown, r: Package) => (
              <Space>
                <Button
                  type="text"
                  size="small"
                  onClick={() =>
                    navigate(`/jabali-admin/packages/edit/${r.id}`)
                  }
                >
                  Edit
                </Button>
                <RowDeleteButton
                  confirmTitle={`Delete package "${r.name}"?`}
                  onConfirm={async () => {
                    await deleteMutation.mutateAsync({ id: r.id });
                  }}
                />
              </Space>
            )}
          />
        </SearchableTableStringQ>
      </Card>
    </div>
  );
};
