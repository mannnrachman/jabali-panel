// AdminBackupsPage — admin overview of every account_backup + restore
// job. Tab layout: Backups (list) / Create Backup (drawer trigger).
// System backup tab lands in SystemBackupsTab when M30 Step 12 ships.
import { Button, Card, Space, Table, Tabs, Tag, Tooltip, Typography, message } from "antd";
import { DownloadOutlined, FileTextOutlined, PlusOutlined, SaveOutlined } from "@icons";
import { useState } from "react";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";
import { useTableURL } from "../../../hooks/useTableURL";
import { BackupLogModal } from "./BackupLogModal";
import { CreateBackupDrawer } from "./CreateBackupDrawer";
import { DestinationsTab } from "./DestinationsTab";
import { EncryptionKeyCard } from "./EncryptionKeyCard";
import { SchedulesTab } from "./SchedulesTab";
import { SystemBackupsTab } from "./SystemBackupsTab";

type BackupJob = {
  id: string;
  user_id: string;
  kind: string;
  status: string;
  systemd_unit: string;
  snapshot_id: string;
  bytes_added: number;
  bytes_total: number;
  created_at: string;
  finished_at?: string;
  error_text?: string;
};

const statusColor = (status: string): string => {
  switch (status) {
    case "succeeded":
      return "green";
    case "running":
      return "blue";
    case "queued":
      return "default";
    case "partial":
      return "gold";
    case "cancelled":
      return "default";
    case "failed":
      return "red";
    default:
      return "default";
  }
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

export const AdminBackupsPage = () => {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [logJob, setLogJob] = useState<BackupJob | null>(null);

  const query = useTableURL<BackupJob>({
    resource: "admin/backups",
    defaultSort: "created_at",
    defaultOrder: "desc",
  });

  const handleDownload = (row: BackupJob) => {
    if (row.status !== "succeeded") {
      message.warning("Backup must complete before download");
      return;
    }
    const url = `/api/v1/admin/backups/${row.id}/download`;
    window.location.href = url;
  };

  const handleCancel = async (row: BackupJob) => {
    try {
      await apiClient.post(`/api/v1/admin/backups/${row.id}/cancel`);
      message.success(`Cancellation requested for ${row.id}`);
      query.refetch();
    } catch (err) {
      message.error(extractApiError(err, "Cancel failed"));
    }
  };

  return (
    <div>
      <Space
        wrap
        align="center"
        style={{ marginBottom: 16, width: "100%", justifyContent: "space-between" }}
      >
        <Typography.Title level={2} style={{ margin: 0 }}>
          <SaveOutlined style={{ marginRight: 8 }} />
          Backups
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setDrawerOpen(true)}
        >
          Create Backup
        </Button>
      </Space>

      <Tabs
        defaultActiveKey="account"
        items={[
          {
            key: "account",
            label: "Account backups",
            children: (
              <Card>
                <SearchableTableStringQ<BackupJob>
                  rowKey="id"
                  loading={query.isLoading}
                  dataSource={query.items}
                  initialSearch={query.params.q}
                  searchPlaceholder="Search by user-id or job-id..."
                  onSearchChange={(q) => query.setParams({ q, page: 1 })}
                  pagination={{
                    current: query.params.page,
                    pageSize: query.params.pageSize,
                    total: query.total,
                  }}
                  scroll={{ x: "max-content" }}
                >
                  <Table.Column
                    dataIndex="id"
                    title="Job ID"
                    render={(id: string) => (
                      <Tooltip title={id}>
                        <code>{id.slice(0, 8)}…</code>
                      </Tooltip>
                    )}
                  />
                  <Table.Column dataIndex="kind" title="Kind" />
                  <Table.Column
                    dataIndex="status"
                    title="Status"
                    render={(s: string) => <Tag color={statusColor(s)}>{s}</Tag>}
                  />
                  <Table.Column
                    dataIndex="bytes_added"
                    title="Added (dedup win)"
                    render={(n: number) => formatBytes(n)}
                  />
                  <Table.Column
                    dataIndex="bytes_total"
                    title="Logical size"
                    render={(n: number) => formatBytes(n)}
                  />
                  <Table.Column dataIndex="created_at" title="Created" />
                  <Table.Column<BackupJob>
                    title="Actions"
                    render={(_, row) => (
                      <Space>
                        <Button
                          size="small"
                          icon={<FileTextOutlined />}
                          onClick={() => setLogJob(row)}
                        >
                          Log
                        </Button>
                        {row.status === "succeeded" && (
                          <Button
                            size="small"
                            icon={<DownloadOutlined />}
                            onClick={() => handleDownload(row)}
                          >
                            Download
                          </Button>
                        )}
                        {row.status === "running" && (
                          <Button size="small" danger onClick={() => handleCancel(row)}>
                            Cancel
                          </Button>
                        )}
                      </Space>
                    )}
                  />
                </SearchableTableStringQ>
              </Card>
            ),
          },
          {
            key: "system",
            label: "System backups",
            children: <SystemBackupsTab />,
          },
          {
            key: "destinations",
            label: "Destinations",
            children: <DestinationsTab />,
          },
          {
            key: "schedules",
            label: "Schedules",
            children: <SchedulesTab />,
          },
          {
            key: "encryption",
            label: "Encryption key",
            children: <EncryptionKeyCard />,
          },
        ]}
      />

      <CreateBackupDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        onCreated={() => {
          setDrawerOpen(false);
          query.refetch();
        }}
      />

      <BackupLogModal
        job={logJob}
        onClose={() => setLogJob(null)}
      />
    </div>
  );
};
