// SystemBackupsTab — admin UI for system_backup snapshots. Per ADR-0075
// the Restore button is intentionally absent: system restore is CLI-only
// in v1 (`jabali system restore --force …`). The card surfaces the
// command line pre-filled with the most recent successful snapshot ID.
import { Alert, Button, Card, Space, Table, Tag, Typography, message } from "antd";
import { FileTextOutlined, ServerOutlined } from "@icons";
import { useState } from "react";

import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";
import { useListQuery } from "../../../hooks/useQueries";
import { BackupLogModal } from "./BackupLogModal";

type SystemBackup = {
  id: string;
  kind: string;
  status: string;
  bytes_total: number;
  bytes_added: number;
  snapshot_id: string;
  created_at: string;
};

interface SystemBackupCreatePayload {
  include_accounts: boolean;
}

const statusColor = (s: string): string => {
  switch (s) {
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

export const SystemBackupsTab = () => {
  const [submitting, setSubmitting] = useState(false);
  const [logJob, setLogJob] = useState<SystemBackup | null>(null);
  const query = useListQuery<SystemBackup>({
    resource: "admin/system/backups",
  });

  const handleCreate = async () => {
    setSubmitting(true);
    try {
      const payload: SystemBackupCreatePayload = { include_accounts: true };
      await apiClient.post("/api/v1/admin/system/backups", payload);
      message.success("System backup queued");
      query.refetch();
    } catch (err) {
      message.error(extractApiError(err, "Create failed"));
    } finally {
      setSubmitting(false);
    }
  };

  const latestSucceeded = (query.items ?? []).find((b) => b.status === "succeeded");

  return (
    <Card
      title={
        <span>
          <ServerOutlined style={{ marginRight: 8 }} />
          System backups
        </span>
      }
      extra={
        <Button type="primary" loading={submitting} onClick={handleCreate}>
          Create system backup
        </Button>
      }
    >
      <Alert
        type="warning"
        showIcon
        message="System restore is CLI-only (ADR-0075)"
        description={
          latestSucceeded ? (
            <div>
              <Typography.Paragraph style={{ marginBottom: 8 }}>
                Run on a fresh OS install:
              </Typography.Paragraph>
              <Typography.Text code copyable>
                jabali system restore --snapshot={latestSucceeded.snapshot_id}{" "}
                --include-accounts --force
              </Typography.Text>
            </div>
          ) : (
            "Once a system backup completes, this card will print the exact restore command with the snapshot ID pre-filled."
          )
        }
        style={{ marginBottom: 16 }}
      />
      <Table<SystemBackup>
        rowKey="id"
        loading={query.isLoading}
        dataSource={query.items ?? []}
        pagination={{ pageSize: 10 }}
        scroll={{ x: "max-content" }}
        columns={[
          { title: "Created", dataIndex: "created_at" },
          {
            title: "Status",
            dataIndex: "status",
            render: (s: string) => <Tag color={statusColor(s)}>{s}</Tag>,
          },
          { title: "Snapshot", dataIndex: "snapshot_id", render: (s: string) => s.slice(0, 12) },
          { title: "Bytes added", dataIndex: "bytes_added" },
          {
            title: "Actions",
            render: (_: unknown, row: SystemBackup) => (
              <Space>
                <Button size="small" icon={<FileTextOutlined />} onClick={() => setLogJob(row)}>
                  Log
                </Button>
              </Space>
            ),
          },
        ]}
      />
      <BackupLogModal
        job={logJob ? { id: logJob.id, kind: logJob.kind || "system_backup", status: logJob.status } : null}
        onClose={() => setLogJob(null)}
      />
    </Card>
  );
};
