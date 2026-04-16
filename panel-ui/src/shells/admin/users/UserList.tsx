// Users list — AntD Table driven by Refine's useTable hook.
//
// Refine's useTable handles pagination state, loading, and sorter/filter
// sync with the URL. We bring our own columns + action column; AntD's
// table prop wiring just works once you hand it `tableProps`.
import { useTable, EditButton, CreateButton } from "@refinedev/antd";
import { Space, Table, Tag, Typography } from "antd";
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
  const { tableProps } = useTable<User>({
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

      <Table<User> {...tableProps} rowKey="id" bordered>
        <Table.Column<User> dataIndex="email" title="Email" />
        <Table.Column<User>
          title="Name"
          render={(_, r) =>
            [r.name_first, r.name_last].filter(Boolean).join(" ") || "—"
          }
        />
        <Table.Column<User>
          dataIndex="is_admin"
          title="Role"
          render={(isAdmin) =>
            isAdmin ? <Tag color="red">admin</Tag> : <Tag>user</Tag>
          }
        />
        <Table.Column<User>
          dataIndex="created_at"
          title="Created"
          render={(ts: string) => new Date(ts).toLocaleString()}
        />
        <Table.Column<User>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => (
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
      </Table>
    </div>
  );
};
