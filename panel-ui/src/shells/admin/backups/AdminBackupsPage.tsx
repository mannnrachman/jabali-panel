// AdminBackupsPage — admin overview of every account_backup +
// system_backup job plus the destinations / schedules / encryption /
// settings sub-pages. Card.tabList renders the tab strip visually
// attached to the card body, matching the Users page.
import { Button, Card, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import {
  CalendarCheckOutlined,
  CloudServerOutlined,
  DownloadOutlined,
  FileTextOutlined,
  KeyOutlined,
  PlusOutlined,
  SaveOutlined,
  ServerOutlined,
  SettingOutlined,
  TeamOutlined,
} from "@icons";
import { useEffect, useState } from "react";

import { BackupStatusTag } from "./BackupStatusTag";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";
import { useTableURL } from "../../../hooks/useTableURL";
import { BackupLogModal } from "./BackupLogModal";
import { BackupSettingsTab } from "./BackupSettingsTab";
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

type TabKey =
  | "account"
  | "system"
  | "destinations"
  | "schedules"
  | "encryption"
  | "settings";

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
  const [activeTab, setActiveTab] = useState<TabKey>("account");

  const query = useTableURL<BackupJob>({
    resource: "admin/backups",
    defaultSort: "created_at",
    defaultOrder: "desc",
  });

  // Always poll on the account tab so newly enqueued jobs surface
  // without F5. Fast tick (3s) when something is queued/running,
  // slow tick (8s) otherwise. No immediate refetch on mount —
  // initial useQuery load already filled the table.
  const hasActive = query.items.some(
    (j) => j.status === "queued" || j.status === "running",
  );
  useEffect(() => {
    if (activeTab !== "account") return;
    const interval = hasActive ? 3000 : 8000;
    const t = window.setInterval(() => {
      query.refetch();
    }, interval);
    return () => window.clearInterval(t);
  }, [activeTab, hasActive, query]);

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
      await apiClient.post(`/admin/backups/${row.id}/cancel`);
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
        <Typography.Title level={3} style={{ margin: 0 }}>
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

      <Card
        tabList={[
          {
            key: "account",
            tab: (
              <Space>
                <TeamOutlined />
                Account backups
                <Tag>{query.total}</Tag>
              </Space>
            ),
          },
          {
            key: "system",
            tab: (
              <Space>
                <ServerOutlined />
                System backups
              </Space>
            ),
          },
          {
            key: "destinations",
            tab: (
              <Space>
                <CloudServerOutlined />
                Destinations
              </Space>
            ),
          },
          {
            key: "schedules",
            tab: (
              <Space>
                <CalendarCheckOutlined />
                Schedules
              </Space>
            ),
          },
          {
            key: "encryption",
            tab: (
              <Space>
                <KeyOutlined />
                Encryption key
              </Space>
            ),
          },
          {
            key: "settings",
            tab: (
              <Space>
                <SettingOutlined />
                Settings
              </Space>
            ),
          },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as TabKey)}
      >
        {activeTab === "account" && (
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
            <Table.Column
              dataIndex="status"
              title="Status"
              render={(s: string) => <BackupStatusTag status={s} />}
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
        )}
        {activeTab === "system" && <SystemBackupsTab />}
        {activeTab === "destinations" && <DestinationsTab />}
        {activeTab === "schedules" && <SchedulesTab />}
        {activeTab === "encryption" && <EncryptionKeyCard />}
        {activeTab === "settings" && <BackupSettingsTab />}
      </Card>

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
