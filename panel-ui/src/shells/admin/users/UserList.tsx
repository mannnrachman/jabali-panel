// Users list — AntD Table driven by Refine's useTable hook, wrapped in
// SearchableTable for debounced server-side search + pagination polish.
//
// Refine's useTable handles pagination state, loading, filter + sorter
// state, and syncs them with the URL. The backend allowlist governs
// *which* columns are searchable/sortable (see panel-api
// userListCols), so the server silently ignores unknown sort keys.
import { useTable, EditButton, CreateButton } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { UserDeleteAction } from "./UserDeleteAction";
import { UserImpersonateAction } from "./UserImpersonateAction";

type User = {
  id: string;
  email: string;
  name_first: string;
  name_last: string;
  is_admin: boolean;
  created_at: string;
};

export const UserList = () => {
  const { tableProps, setFilters } = useTable<User>({
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
          Users
        </Typography.Title>
        <CreateButton />
      </Space>

      <SearchableTable<User>
        {...tableProps}
        rowKey="id"
        bordered
        searchPlaceholder="Search by email, username, or name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column
          dataIndex="email"
          title="Email"
          sorter={{ multiple: 1 }}
        />
        <Table.Column
          title="Name"
          render={(_: unknown, r: User) =>
            [r.name_first, r.name_last].filter(Boolean).join(" ") || "—"
          }
        />
        <Table.Column
          dataIndex="is_admin"
          title="Role"
          sorter={{ multiple: 1 }}
          render={(isAdmin: boolean) =>
            isAdmin ? <Tag color="red">admin</Tag> : <Tag>user</Tag>
          }
        />
        <Table.Column
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 1 }}
          defaultSortOrder="descend"
          render={(ts: string) => new Date(ts).toLocaleString()}
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
              <EditButton hideText size="small" recordItemId={r.id} />
              <UserDeleteAction recordItemId={r.id} userEmail={r.email} />
            </Space>
          )}
        />
      </SearchableTable>
    </div>
  );
};
