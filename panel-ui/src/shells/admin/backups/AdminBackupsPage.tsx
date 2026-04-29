// AdminBackupsPage — admin overview of every backup run.
// Scheduler-fired jobs roll up under their run_id (one parent row,
// expandable to per-user children). Manual creates render flat.
import { Button, Card, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import {
  CalendarCheckOutlined,
  DownloadOutlined,
  FileTextOutlined,
  HardDriveUploadOutlined,
  KeyOutlined,
  PlusOutlined,
  RotateCcwOutlined,
  SaveOutlined,
  SettingOutlined,
} from "@icons";
import { useEffect, useState } from "react";

import { BackupStatusTag } from "./BackupStatusTag";

import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";
import { BackupLogModal } from "./BackupLogModal";
import { BackupSettingsTab } from "./BackupSettingsTab";
import { CreateBackupDrawer } from "./CreateBackupDrawer";
import { DestinationsTab } from "./DestinationsTab";
import { EncryptionKeyCard } from "./EncryptionKeyCard";
import { SchedulesTab } from "./SchedulesTab";

interface BackupJob {
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
  run_id?: string;
}

interface BackupRun {
  run_id: string;
  schedule_id?: string;
  kind: string;
  total: number;
  succeeded: number;
  failed: number;
  running: number;
  queued: number;
  cancelled: number;
  partial: number;
  bytes_added: number;
  bytes_total: number;
  started_at: string;
  latest_updated: string;
}

interface RunsEnvelope {
  data: BackupRun[];
  manual: BackupJob[];
  total: number;
  manual_total: number;
}

interface RunRow {
  rowKey: string;
  isRun: true;
  run: BackupRun;
}
interface ManualRow {
  rowKey: string;
  isRun: false;
  job: BackupJob;
}
type TableRow = RunRow | ManualRow;

type TabKey =
  | "backups"
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

const renderTypeTag = (k: string) => {
  const label =
    k === "system_backup"
      ? "System Backup"
      : k === "account_backup"
        ? "Account Backup"
        : k === "system_restore"
          ? "System Restore"
          : k === "account_restore"
            ? "Account Restore"
            : k;
  const color = k.startsWith("system") ? "purple" : "blue";
  return <Tag color={color}>{label}</Tag>;
};

// Run summary collapses 6 status counters into a single Tag stack so
// the row at a glance answers "is this run still working / did any
// fail?" — full breakdown lives in the expanded child table.
const RunStatusSummary = ({ run }: { run: BackupRun }) => {
  const tags: { color: string; text: string }[] = [];
  if (run.running > 0) tags.push({ color: "blue", text: `${run.running} running` });
  if (run.queued > 0) tags.push({ color: "default", text: `${run.queued} queued` });
  if (run.failed > 0) tags.push({ color: "red", text: `${run.failed} failed` });
  if (run.partial > 0) tags.push({ color: "gold", text: `${run.partial} partial` });
  if (run.cancelled > 0) tags.push({ color: "default", text: `${run.cancelled} cancelled` });
  if (run.succeeded > 0) tags.push({ color: "green", text: `${run.succeeded} succeeded` });
  if (tags.length === 0) tags.push({ color: "default", text: `${run.total} jobs` });
  return (
    <Space size={4} wrap>
      {tags.map((t) => (
        <Tag key={t.text} color={t.color}>
          {t.text}
        </Tag>
      ))}
    </Space>
  );
};

export const AdminBackupsPage = () => {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [logJob, setLogJob] = useState<BackupJob | null>(null);
  const [activeTab, setActiveTab] = useState<TabKey>("backups");
  const [runs, setRuns] = useState<BackupRun[]>([]);
  const [manual, setManual] = useState<BackupJob[]>([]);
  const [loading, setLoading] = useState(false);
  const [runJobs, setRunJobs] = useState<Record<string, BackupJob[]>>({});

  const reload = async () => {
    setLoading(true);
    try {
      const resp = await apiClient.get<RunsEnvelope>(
        "/admin/backup-runs?page_size=50",
      );
      setRuns(resp.data.data ?? []);
      setManual(resp.data.manual ?? []);
    } catch (err) {
      message.error(extractApiError(err, "Load failed"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (activeTab === "backups") {
      void reload();
    }
  }, [activeTab]);

  const hasActive =
    runs.some((r) => r.queued + r.running > 0) ||
    manual.some((j) => j.status === "queued" || j.status === "running");
  useEffect(() => {
    if (activeTab !== "backups") return;
    const interval = hasActive ? 3000 : 8000;
    const t = window.setInterval(() => {
      void reload();
    }, interval);
    return () => window.clearInterval(t);
  }, [activeTab, hasActive]);

  const expandRun = async (runID: string) => {
    if (runJobs[runID]) return; // cached
    try {
      const resp = await apiClient.get<{ data: BackupJob[] }>(
        `/admin/backup-runs/${runID}/jobs`,
      );
      setRunJobs((m) => ({ ...m, [runID]: resp.data.data ?? [] }));
    } catch (err) {
      message.error(extractApiError(err, "Load run failed"));
    }
  };

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
      void reload();
    } catch (err) {
      message.error(extractApiError(err, "Cancel failed"));
    }
  };

  const tableRows: TableRow[] = [
    ...runs.map<RunRow>((r) => ({ rowKey: `run:${r.run_id}`, isRun: true, run: r })),
    ...manual.map<ManualRow>((j) => ({ rowKey: `job:${j.id}`, isRun: false, job: j })),
  ].sort((a, b) => {
    const aT = a.isRun ? a.run.latest_updated : a.job.created_at;
    const bT = b.isRun ? b.run.latest_updated : b.job.created_at;
    return aT < bT ? 1 : aT > bT ? -1 : 0;
  });

  const renderChildJobs = (jobs: BackupJob[]) => (
    <Table<BackupJob>
      rowKey="id"
      dataSource={jobs}
      pagination={false}
      size="small"
      columns={[
        {
          title: "Job ID",
          dataIndex: "id",
          render: (id: string) => (
            <Tooltip title={id}>
              <code>{id.slice(0, 8)}…</code>
            </Tooltip>
          ),
        },
        { title: "User", dataIndex: "user_id" },
        {
          title: "Status",
          dataIndex: "status",
          render: (s: string) => <BackupStatusTag status={s} />,
        },
        {
          title: "Added",
          dataIndex: "bytes_added",
          render: (n: number) => formatBytes(n),
        },
        {
          title: "Size",
          dataIndex: "bytes_total",
          render: (n: number) => formatBytes(n),
        },
        {
          title: "Actions",
          render: (_: unknown, row: BackupJob) => (
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
          ),
        },
      ]}
    />
  );

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
            key: "backups",
            tab: (
              <Space>
                <RotateCcwOutlined />
                Backups
                <Tag>{runs.length + manual.length}</Tag>
              </Space>
            ),
          },
          {
            key: "destinations",
            tab: (
              <Space>
                <HardDriveUploadOutlined />
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
        {activeTab === "backups" && (
          <Table<TableRow>
            rowKey="rowKey"
            loading={loading}
            dataSource={tableRows}
            pagination={{ pageSize: 25 }}
            scroll={{ x: "max-content" }}
            expandable={{
              rowExpandable: (row) => row.isRun,
              onExpand: (expanded, row) => {
                if (expanded && row.isRun) void expandRun(row.run.run_id);
              },
              expandedRowRender: (row) =>
                row.isRun ? (
                  runJobs[row.run.run_id] ? (
                    renderChildJobs(runJobs[row.run.run_id])
                  ) : (
                    <Typography.Text type="secondary">Loading…</Typography.Text>
                  )
                ) : null,
            }}
            columns={[
              {
                title: "ID",
                render: (_: unknown, row: TableRow) => {
                  const id = row.isRun ? row.run.run_id : row.job.id;
                  return (
                    <Tooltip title={id}>
                      <code>{id.slice(0, 8)}…</code>
                    </Tooltip>
                  );
                },
              },
              {
                title: "Source",
                render: (_: unknown, row: TableRow) =>
                  row.isRun ? (
                    <Tag color="geekblue">scheduled run</Tag>
                  ) : (
                    <Tag>manual</Tag>
                  ),
              },
              {
                title: "Type",
                render: (_: unknown, row: TableRow) =>
                  renderTypeTag(row.isRun ? row.run.kind : row.job.kind),
              },
              {
                title: "Status",
                render: (_: unknown, row: TableRow) =>
                  row.isRun ? (
                    <RunStatusSummary run={row.run} />
                  ) : (
                    <BackupStatusTag status={row.job.status} />
                  ),
              },
              {
                title: "Added (dedup win)",
                render: (_: unknown, row: TableRow) =>
                  formatBytes(row.isRun ? row.run.bytes_added : row.job.bytes_added),
              },
              {
                title: "Logical size",
                render: (_: unknown, row: TableRow) =>
                  formatBytes(row.isRun ? row.run.bytes_total : row.job.bytes_total),
              },
              {
                title: "Started",
                render: (_: unknown, row: TableRow) =>
                  row.isRun ? row.run.started_at : row.job.created_at,
              },
              {
                title: "Actions",
                render: (_: unknown, row: TableRow) => {
                  if (row.isRun) {
                    return <Typography.Text type="secondary">expand for jobs</Typography.Text>;
                  }
                  return (
                    <Space>
                      <Button
                        size="small"
                        icon={<FileTextOutlined />}
                        onClick={() => setLogJob(row.job)}
                      >
                        Log
                      </Button>
                      {row.job.status === "succeeded" && (
                        <Button
                          size="small"
                          icon={<DownloadOutlined />}
                          onClick={() => handleDownload(row.job)}
                        >
                          Download
                        </Button>
                      )}
                      {row.job.status === "running" && (
                        <Button size="small" danger onClick={() => handleCancel(row.job)}>
                          Cancel
                        </Button>
                      )}
                    </Space>
                  );
                },
              },
            ]}
          />
        )}
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
          void reload();
        }}
      />

      <BackupLogModal
        job={logJob}
        onClose={() => setLogJob(null)}
      />
    </div>
  );
};
