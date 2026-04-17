// Database Users list with grants embedded.
//
// Each row represents a MariaDB user (username + "@localhost"), and
// the "Database Privileges" column renders one tag per grant with an
// × that revokes just that grant. Row-level actions are Add Access
// (open AddGrantModal), Password (rotate + reveal modal), and Delete
// (drops the whole user, cascading all grants).
import { useMemo, useState } from "react";
import { useTable, DeleteButton, CreateButton } from "@refinedev/antd";
import { Button, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import { KeyOutlined, PlusOutlined, UserOutlined } from "@ant-design/icons";

import { apiClient } from "../../../apiClient";
import { SearchableTable } from "../../../components/SearchableTable";
import { readQValue } from "../../../components/searchableTableUtils";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";
import { AddGrantModal } from "../../../components/AddGrantModal";

export type Grant = {
  id: string;
  database_id: string;
  database_name: string;
  grant_level: "rw" | "ro" | "custom";
  privileges?: string;
};

export type DatabaseUser = {
  id: string;
  user_id: string;
  username: string;
  created_at: string;
  updated_at: string;
  grants: Grant[];
};

// Rotate-password calls require the client to POST something in
// new_password even though the server ignores it and generates its
// own. A local random string keeps the body valid.
function placeholderForRotateBody(): string {
  const alphabet =
    "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += alphabet[b % alphabet.length];
  return out;
}

function grantLabel(grant: Grant): string {
  switch (grant.grant_level) {
    case "rw":
      return "Full Access";
    case "ro":
      return "Read only";
    case "custom":
      if (grant.privileges) {
        const privs = grant.privileges.split(",").map((p) => p.trim());
        return privs.length > 2 ? `${privs.slice(0, 2).join(", ")}…` : privs.join(", ");
      }
      return "Custom";
    default:
      return "Unknown";
  }
}

export const DatabaseUsersList = () => {
  const { tableProps, setFilters, filters, tableQueryResult } =
    useTable<DatabaseUser>({
      resource: "database-users",
      syncWithLocation: true,
    });
  const initialSearch = useMemo(() => readQValue(filters), [filters]);

  const [passwordModal, setPasswordModal] = useState<{
    username: string;
    password: string;
    title: string;
  } | null>(null);
  const [rotatingId, setRotatingId] = useState<string | null>(null);
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [grantTarget, setGrantTarget] = useState<DatabaseUser | null>(null);

  const refresh = () => {
    tableQueryResult?.refetch();
  };

  const rotate = async (row: DatabaseUser) => {
    setRotatingId(row.id);
    try {
      const resp = await apiClient.post<{ password: string }>(
        `/database-users/${row.id}/rotate-password`,
        { new_password: placeholderForRotateBody() },
      );
      setPasswordModal({
        username: row.username,
        password: resp.data.password,
        title: "New password (rotation)",
      });
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ?? "Failed to rotate password";
      message.error(msg);
    } finally {
      setRotatingId(null);
    }
  };

  const revokeGrant = async (grant: Grant) => {
    setRevokingId(grant.id);
    try {
      await apiClient.delete(`/database-user-grants/${grant.id}`);
      message.success(`Revoked ${grantLabel(grant).toLowerCase()} on ${grant.database_name}`);
      refresh();
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ?? "Failed to revoke grant";
      message.error(msg);
    } finally {
      setRevokingId(null);
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
        <CreateButton resource="database-users">Create User</CreateButton>
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
          title="User"
          sorter={{ multiple: 1 }}
          defaultSortOrder="ascend"
          render={(username: string) => (
            <Space>
              <UserOutlined />
              <span style={{ fontFamily: "monospace" }}>{username}</span>
              <Typography.Text type="secondary">@localhost</Typography.Text>
            </Space>
          )}
        />
        <Table.Column<DatabaseUser>
          title="Database Privileges"
          dataIndex="grants"
          render={(grants: Grant[] | undefined) => {
            if (!grants || grants.length === 0) {
              return <Typography.Text type="secondary">No grants</Typography.Text>;
            }
            return (
              <Space size={[4, 4]} wrap>
                {grants.map((g) => (
                  <Tag
                    key={g.id}
                    color={g.grant_level === "rw" ? "green" : g.grant_level === "ro" ? "blue" : "orange"}
                    closable
                    onClose={(e) => {
                      e.preventDefault();
                      revokeGrant(g);
                    }}
                    style={{
                      opacity: revokingId === g.id ? 0.5 : 1,
                      pointerEvents: revokingId === g.id ? "none" : undefined,
                    }}
                  >
                    {g.database_name} ({grantLabel(g)})
                  </Tag>
                ))}
              </Space>
            );
          }}
        />
        <Table.Column<DatabaseUser>
          dataIndex="created_at"
          title="Created"
          sorter={{ multiple: 2 }}
          render={(date: string) => new Date(date).toLocaleDateString()}
          width={120}
        />
        <Table.Column<DatabaseUser>
          title="Actions"
          dataIndex="actions"
          width={180}
          render={(_, row) => (
            <Space size="small">
              <Tooltip title="Add database access">
                <Button
                  size="small"
                  type="text"
                  icon={<PlusOutlined />}
                  onClick={() => setGrantTarget(row)}
                />
              </Tooltip>
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
                onSuccess={refresh}
              />
            </Space>
          )}
        />
      </SearchableTable>

      <DatabaseUserPasswordModal
        open={passwordModal !== null}
        username={passwordModal?.username ?? ""}
        password={passwordModal?.password ?? ""}
        title={passwordModal?.title ?? "Database user password"}
        onClose={() => setPasswordModal(null)}
      />

      <AddGrantModal
        open={grantTarget !== null}
        userId={grantTarget?.id ?? null}
        username={grantTarget?.username ?? ""}
        excludedDatabaseIds={
          grantTarget?.grants?.map((g) => g.database_id) ?? []
        }
        onClose={() => setGrantTarget(null)}
        onSuccess={refresh}
      />
    </div>
  );
};
