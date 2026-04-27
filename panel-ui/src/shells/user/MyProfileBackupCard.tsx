// MyProfileBackupCard — user-shell self-backup card. Generate full
// backup of the caller's account; list recent self-backups; download
// when a row is succeeded. Mirrors AdminBackupsPage data shape but
// scoped via /me/backups (auth-gated to caller's user_id).
import { Button, Card, Space, Table, Tag, Typography, message } from "antd";
import { DownloadOutlined, SaveOutlined } from "@icons";
import { useState } from "react";

import { apiClient } from "../../apiClient";
import { useListQuery } from "../../hooks/useQueries";

type MyBackup = {
  id: string;
  status: string;
  bytes_total: number;
  bytes_added: number;
  created_at: string;
};

const formatBytes = (n: number): string => {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(1)} ${units[i]}`;
};

const statusColor = (status: string): string => {
  switch (status) {
    case "succeeded":
      return "green";
    case "running":
      return "blue";
    case "failed":
      return "red";
    case "partial":
      return "gold";
    default:
      return "default";
  }
};

export const MyProfileBackupCard = () => {
  const [submitting, setSubmitting] = useState(false);
  const query = useListQuery<MyBackup>({ resource: "me/backups" });

  const handleCreate = async () => {
    setSubmitting(true);
    try {
      await apiClient.post("/api/v1/me/backups");
      message.success("Backup queued");
      query.refetch();
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Create failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card
      title={
        <span>
          <SaveOutlined style={{ marginRight: 8 }} />
          My backups
        </span>
      }
      extra={
        <Button type="primary" loading={submitting} onClick={handleCreate}>
          Generate full backup
        </Button>
      }
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        A full backup bundles your home directory, databases, and mailboxes
        into a portable tar.zst you can download. Backups are deduplicated
        on the server, so repeat runs only store what actually changed.
      </Typography.Paragraph>
      <Table<MyBackup>
        rowKey="id"
        loading={query.isLoading}
        dataSource={query.items ?? []}
        pagination={{ pageSize: 10 }}
        scroll={{ x: "max-content" }}
        columns={[
          {
            title: "Created",
            dataIndex: "created_at",
            render: (t: string) => t,
          },
          {
            title: "Status",
            dataIndex: "status",
            render: (s: string) => <Tag color={statusColor(s)}>{s}</Tag>,
          },
          {
            title: "Size",
            dataIndex: "bytes_total",
            render: (n: number) => formatBytes(n),
          },
          {
            title: "Actions",
            key: "actions",
            render: (_, row) =>
              row.status === "succeeded" ? (
                <Space>
                  <Button
                    size="small"
                    icon={<DownloadOutlined />}
                    href={`/api/v1/admin/backups/${row.id}/download`}
                  >
                    Download
                  </Button>
                </Space>
              ) : null,
          },
        ]}
      />
    </Card>
  );
};
