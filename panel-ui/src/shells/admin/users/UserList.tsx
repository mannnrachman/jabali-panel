// Users + Administrators — split into two AntD card-style tabs. Each
// tab is its own useTableURL instance, scoped server-side via the
// hook's `extraParams.is_admin`. Tabs unmount inactive content by
// default (AntD), so the two useTableURL calls never run concurrently
// and their URL params don't collide either.
//
// Backend allowlist governs which columns are searchable/sortable;
// the ?is_admin filter is applied before search/sort so the paginated
// total stays correct per tab.
import { useState } from "react";
import { Button, Card, Input, Space, Table, Tag, Typography } from "antd";
import { SearchOutlined } from "@ant-design/icons";
import type { SorterResult } from "antd/es/table/interface";
import { useNavigate } from "react-router";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useListQuery } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
import { UserDeleteAction } from "./UserDeleteAction";
import { UserSliceStatus } from "./UserSliceStatus";

type User = {
  id: string;
  email: string;
  // POSIX account name for regular users; NULL/absent for admins.
  username?: string | null;
  name_first: string;
  name_last: string;
  is_admin: boolean;
  created_at: string;
};

const renderName = (_: unknown, r: User) =>
  [r.name_first, r.name_last].filter(Boolean).join(" ");

const renderCreated = (ts: string) => new Date(ts).toLocaleString();

// Shared row-action buttons for both tables. Wired to react-router
// directly — no <EditButton> wrapper around a plain <Button>.
//
// Button copy intentionally does NOT include the row's email: the
// users-spec E2E asserts on `getByRole("cell", { name: email })`, and
// if the action cell's accessible name contained the email too, the
// matcher would hit both cells and fail with a strict-mode violation.
function RowActions({ user }: { user: User }) {
  const navigate = useNavigate();
  return (
    <Space size="middle">
      <Button
        type="text"
        onClick={() => navigate(`/jabali-admin/users/edit/${user.id}`)}
      >
        Edit
      </Button>
      <UserDeleteAction recordItemId={user.id} userEmail={user.email} />
    </Space>
  );
}

type UsersTableProps = {
  isAdmin: boolean;
  searchPlaceholder: string;
  showSliceColumn: boolean;
};

function UsersShellTable({
  isAdmin,
  searchPlaceholder,
  showSliceColumn,
}: UsersTableProps) {
  const query = useTableURL<User>({
    resource: "users",
    defaultSort: "created_at",
    defaultOrder: "desc",
    extraParams: { is_admin: String(isAdmin) },
  });

  // AntD Table's onChange emits the current pagination + sorter;
  // project that back into useTableURL's params so the URL stays
  // the single source of truth.
  const handleTableChange: React.ComponentProps<typeof Table<User>>["onChange"] = (
    pagination,
    _filters,
    sorter,
  ) => {
    const single = Array.isArray(sorter)
      ? (sorter[0] as SorterResult<User> | undefined)
      : (sorter as SorterResult<User>);
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
    <SearchableTableStringQ<User>
      rowKey="id"
      loading={query.isLoading}
      dataSource={query.items}
      initialSearch={query.params.q}
      searchPlaceholder={searchPlaceholder}
      onSearchChange={(q) => query.setParams({ q, page: 1 })}
      pagination={{
        current: query.params.page,
        pageSize: query.params.pageSize,
        total: query.total,
      }}
      onChange={handleTableChange}
    >
      <Table.Column
        dataIndex="email"
        title="Email"
        key="email"
        sorter={{ multiple: 1 }}
        filterIcon={() => (
          <SearchOutlined
            style={{ color: query.params.q ? "#ef4444" : undefined }}
          />
        )}
        filterDropdown={({ confirm, close }) => (
          <div style={{ padding: 8, minWidth: 240 }}>
            <Input.Search
              placeholder={searchPlaceholder}
              allowClear
              defaultValue={query.params.q}
              onSearch={(value) => {
                query.setParams({ q: value.trim(), page: 1 });
                confirm({ closeDropdown: false });
                close();
              }}
            />
          </div>
        )}
      />
      <Table.Column<User>
        dataIndex="username"
        title="Username"
        key="username"
        render={(v: string | null | undefined) =>
          v ? (
            <Typography.Text style={{ fontFamily: "monospace" }}>
              {v}
            </Typography.Text>
          ) : (
            <Typography.Text type="secondary">—</Typography.Text>
          )
        }
        filterIcon={() => (
          <SearchOutlined
            style={{ color: query.params.q ? "#ef4444" : undefined }}
          />
        )}
        filterDropdown={({ confirm, close }) => (
          <div style={{ padding: 8, minWidth: 240 }}>
            <Input.Search
              placeholder={searchPlaceholder}
              allowClear
              defaultValue={query.params.q}
              onSearch={(value) => {
                query.setParams({ q: value.trim(), page: 1 });
                confirm({ closeDropdown: false });
                close();
              }}
            />
          </div>
        )}
      />
      <Table.Column
        title="Name"
        render={renderName}
        filterIcon={() => (
          <SearchOutlined
            style={{ color: query.params.q ? "#ef4444" : undefined }}
          />
        )}
        filterDropdown={({ confirm, close }) => (
          <div style={{ padding: 8, minWidth: 240 }}>
            <Input.Search
              placeholder={searchPlaceholder}
              allowClear
              defaultValue={query.params.q}
              onSearch={(value) => {
                query.setParams({ q: value.trim(), page: 1 });
                confirm({ closeDropdown: false });
                close();
              }}
            />
          </div>
        )}
      />
      <Table.Column
        dataIndex="created_at"
        title="Created"
        key="created_at"
        sorter={{ multiple: 1 }}
        defaultSortOrder="descend"
        render={renderCreated}
      />
      {showSliceColumn && (
        <Table.Column
          title="Slice"
          render={(_: unknown, r: User) => <UserSliceStatus userId={r.id} />}
        />
      )}
      <Table.Column
        title="Actions"
        dataIndex="actions"
        render={(_: unknown, r: User) => <RowActions user={r} />}
      />
    </SearchableTableStringQ>
  );
}

export const UserList = () => {
  const [activeTab, setActiveTab] = useState<"users" | "admins">("users");
  const navigate = useNavigate();

  // Tab-label badges need totals for BOTH roles regardless of which
  // tab is active. Tabs unmount inactive content, so the per-tab
  // useTableURL can't tell us the inactive count — fetch each total
  // here with a pageSize=1 list so the payload is just the count +
  // one row.
  const usersCountQ = useListQuery<User>({
    resource: "users",
    params: { pageSize: 1, is_admin: "false" },
  });
  const adminsCountQ = useListQuery<User>({
    resource: "users",
    params: { pageSize: 1, is_admin: "true" },
  });

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
          Users
        </Typography.Title>
        <Button
          type="primary"
          onClick={() => navigate("/jabali-admin/users/create")}
        >
          Create
        </Button>
      </Space>

      {/* Card.tabList renders the tab strip visually attached to the
          card body — gives the connected "tab → panel" look the bare
          Tabs component lacks. activeTabKey drives which child table
          renders. */}
      <Card
        tabList={[
          {
            key: "users",
            tab: (
              <Space>
                Users
                <Tag>{usersCountQ.total}</Tag>
              </Space>
            ),
          },
          {
            key: "admins",
            tab: (
              <Space>
                Administrators
                <Tag>{adminsCountQ.total}</Tag>
              </Space>
            ),
          },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as "users" | "admins")}
      >
        {activeTab === "users" ? (
          <UsersShellTable
            isAdmin={false}
            searchPlaceholder="Search by email, username, or name"
            showSliceColumn
          />
        ) : (
          <UsersShellTable
            isAdmin
            searchPlaceholder="Search by email or name"
            showSliceColumn={false}
          />
        )}
      </Card>
    </div>
  );
};
