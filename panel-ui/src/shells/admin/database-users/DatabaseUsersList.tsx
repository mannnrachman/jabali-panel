// Admin list of database users.
//
// Renders username + created_at with row actions:
//   - Rotate password (generates new password server-side-ish;
//     client generates, server stores bcrypt hash, response returns
//     the plaintext once)
//   - Delete (drops the user in MariaDB and cascades grants)
//
// The list intentionally does NOT show grants yet — the backend list
// endpoint returns plain DatabaseUser rows without embedded grants.
// Showing "user alice → database X (rw)" will require extending
// database_users.go list to batch-fetch DatabaseUserGrants. Tracked as
// a follow-up.
import { useMemo, useState } from "react";
import { useTable, DeleteButton, CreateButton } from "@refinedev/antd";
import { Button, Space, Table, Tooltip, Typography } from "antd";
import { KeyOutlined, UserOutlined } from "@ant-design/icons";

import { apiClient } from "../../../apiClient";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";

export type DatabaseUser = {
  id: string;
  user_id: string;
  username: string;
  created_at: string;
  updated_at: string;
};

// Generate a 24-char password client-side using Web Crypto. Kept local
// to this file — the password is ephemeral and never leaves the client
// except as the bcrypt hash the server computes on POST.
function generatePassword(): string {
  const alphabet =
    "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += alphabet[b % alphabet.length];
  return out;
}

export const DatabaseUsersList = () => {
  const { tableProps, setFilters, filters } = useTable<DatabaseUser>({
    resource: "database-users",
    syncWithLocation: true,
  });
  const initialSearch = useMemo(() => readQValue(filters), [filters]);

  const [revealedPassword, setRevealedPassword] = useState<{
    username: string;
    password: string;
  } | null>(null);
  const [rotatingId, setRotatingId] = useState<string | null>(null);

  const rotate = async (row: DatabaseUser) => {
    setRotatingId(row.id);
    try {
      const newPassword = generatePassword();
      const resp = await apiClient.post<{ password: string }>(
        `/database-users/${row.id}/rotate-password`,
        { new_password: newPassword },
      );
      setRevealedPassword({
        username: row.username,
        password: resp.data.password,
      });
    } finally {
      setRotatingId(null);
    }
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
          Database Users
        </Typography.Title>
        <CreateButton>Create User</CreateButton>
      </Space>

      <SearchableTable<DatabaseUser>
        {...tableProps}
        rowKey="id"
        bordered
        initialSearch={initialSearch}
        searchPlaceholder="Search by username"
        onSearchChange={(f) => setFilters(f, "replace")}
      >
        <Table.Column<DatabaseUser>
          dataIndex="username"
          title="Username"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(username: string) => (
            <Space>
              <UserOutlined />
              <span style={{ fontFamily: "monospace" }}>{username}</span>
            </Space>
          )}
        />
        <Table.Column<DatabaseUser>
          dataIndex="user_id"
          title="Owner"
          render={(v: string) => v.substring(0, 8)}
        />
        <Table.Column<DatabaseUser>
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          render={(date: string) => new Date(date).toLocaleDateString()}
        />
        <Table.Column<DatabaseUser>
          title="Actions"
          dataIndex="actions"
          render={(_, row) => (
            <Space size="small">
              <Tooltip title="Rotate password">
                <Button
                  size="small"
                  type="text"
                  icon={<KeyOutlined />}
                  loading={rotatingId === row.id}
                  onClick={() => rotate(row)}
                />
              </Tooltip>
              <DeleteButton
                hideText
                size="small"
                type="text"
                resource="database-users"
                recordItemId={row.id}
              />
            </Space>
          )}
        />
      </SearchableTable>

      <DatabaseUserPasswordModal
        open={revealedPassword !== null}
        username={revealedPassword?.username ?? ""}
        password={revealedPassword?.password ?? ""}
        title="New password (rotation)"
        onClose={() => setRevealedPassword(null)}
      />
    </div>
  );
};
