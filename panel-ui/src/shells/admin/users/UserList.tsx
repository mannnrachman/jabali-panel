// Users + Administrators — split into two AntD card-style tabs. Each tab
// is its own Refine useTable instance, scoped server-side via the
// dataProvider's meta.params.is_admin passthrough. Tabs unmount inactive
// content by default (AntD), so the two useTable calls never run
// concurrently and there's no URL/pagination clash.
//
// Backend allowlist governs which columns are searchable/sortable; the
// new ?is_admin filter is applied before search/sort, so the paginated
// total stays correct per tab.
import { useState } from "react";
import { useTable, EditButton, CreateButton } from "@refinedev/antd";
import { Space, Table, Tabs, Typography } from "antd";
import { TeamOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { UserDeleteAction } from "./UserDeleteAction";
import { UserImpersonateAction } from "./UserImpersonateAction";
import { UserSliceStatus } from "./UserSliceStatus";

type User = {
  id: string;
  email: string;
  name_first: string;
  name_last: string;
  is_admin: boolean;
  created_at: string;
};

const renderName = (_: unknown, r: User) =>
  [r.name_first, r.name_last].filter(Boolean).join(" ") || "—";

const renderCreated = (ts: string) => new Date(ts).toLocaleString();

// UsersTable — non-admins only. Keeps the Slice column (only meaningful
// for users with a Linux account) and the Impersonate action.
const UsersTable = () => {
  const { tableProps, setFilters, filters } = useTable<User>({
    syncWithLocation: true,
    meta: { params: { is_admin: "false" } },
  });
  const initialSearch = readQValue(filters);

  return (
    <SearchableTable<User>
      {...tableProps}
      rowKey="id"
      bordered
      initialSearch={initialSearch}
      searchPlaceholder="Search by email, username, or name"
      onSearchChange={(filters) => setFilters(filters, "replace")}
    >
      <Table.Column dataIndex="email" title="Email" sorter={{ multiple: 1 }} />
      <Table.Column title="Name" render={renderName} />
      <Table.Column
        dataIndex="created_at"
        title="Created"
        sorter={{ multiple: 1 }}
        defaultSortOrder="descend"
        render={renderCreated}
      />
      <Table.Column
        title="Slice"
        render={(_: unknown, r: User) => <UserSliceStatus userId={r.id} />}
      />
      <Table.Column
        title="Actions"
        dataIndex="actions"
        render={(_: unknown, r: User) => (
          <Space>
            <UserImpersonateAction
              recordItemId={r.id}
              userEmail={r.email}
              isAdmin={r.is_admin}
            />
            <EditButton hideText size="small" type="text" recordItemId={r.id} />
            <UserDeleteAction recordItemId={r.id} userEmail={r.email} />
          </Space>
        )}
      />
    </SearchableTable>
  );
};

// AdministratorsTable — admins only. No Slice column (admins don't get
// a Linux account / per-user FPM slice) and no Impersonate action
// (ADR-0015 — admins cannot be impersonated).
const AdministratorsTable = () => {
  const { tableProps, setFilters, filters } = useTable<User>({
    syncWithLocation: true,
    meta: { params: { is_admin: "true" } },
  });
  const initialSearch = readQValue(filters);

  return (
    <SearchableTable<User>
      {...tableProps}
      rowKey="id"
      bordered
      initialSearch={initialSearch}
      searchPlaceholder="Search by email or name"
      onSearchChange={(filters) => setFilters(filters, "replace")}
    >
      <Table.Column dataIndex="email" title="Email" sorter={{ multiple: 1 }} />
      <Table.Column title="Name" render={renderName} />
      <Table.Column
        dataIndex="created_at"
        title="Created"
        sorter={{ multiple: 1 }}
        defaultSortOrder="descend"
        render={renderCreated}
      />
      <Table.Column
        title="Actions"
        dataIndex="actions"
        render={(_: unknown, r: User) => (
          <Space>
            <EditButton hideText size="small" type="text" recordItemId={r.id} />
            <UserDeleteAction recordItemId={r.id} userEmail={r.email} />
          </Space>
        )}
      />
    </SearchableTable>
  );
};

export const UserList = () => {
  const [activeTab, setActiveTab] = useState<"users" | "admins">("users");

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
          Users
        </Typography.Title>
        <CreateButton />
      </Space>

      <Tabs
        type="card"
        activeKey={activeTab}
        onChange={(k) => setActiveTab(k as "users" | "admins")}
        items={[
          {
            key: "users",
            label: (
              <span>
                <TeamOutlined /> Users
              </span>
            ),
            children: <UsersTable />,
          },
          {
            key: "admins",
            label: (
              <span>
                <SafetyCertificateOutlined /> Administrators
              </span>
            ),
            children: <AdministratorsTable />,
          },
        ]}
      />
    </div>
  );
};
