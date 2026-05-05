import { useState } from "react";
import {
  Alert,
  DatePicker,
  Input,
  Space,
  Table,
  Typography,
  Tag,
  Button,
  Modal,
  Spin,
} from "antd";
import { ReloadOutlined, FileTextOutlined } from "@ant-design/icons";
import dayjs, { Dayjs } from "dayjs";
import { useListQuery } from "../../../hooks/useQueries";
import { apiClient } from "../../../apiClient";

interface BackupLogEntry {
  id: string;
  kind: string;
  status: string;
  user_id: string;
  destination_id?: string;
  created_at: string;
  finished_at?: string;
  error_text?: string;
}

interface BackupLogDetails {
  unit: string;
  status: string;
  exit_code?: number;
  log_text: string;
  fetched_at: string;
}

interface Filters {
  from?: Dayjs | null;
  to?: Dayjs | null;
  kind?: string;
  status?: string;
  user_id?: string;
}

export const BackupLogsTab = () => {
  const [filters, setFilters] = useState<Filters>({});
  const [page, setPage] = useState(1);
  const [selectedLog, setSelectedLog] = useState<BackupLogEntry | null>(null);
  const [logDetails, setLogDetails] = useState<BackupLogDetails | null>(null);
  const [loadingDetails, setLoadingDetails] = useState(false);
  const pageSize = 50;

  // Fetch backup and restore jobs
  const { data, isLoading, error, refetch, isFetching } = useListQuery<BackupLogEntry>({
    resource: "admin/backup-runs",
    params: {
      page_size: 200, // Get more entries for logs
    },
  });

  const usersQuery = useListQuery<{ id: string; username: string; email: string }>({
    resource: "users",
    params: { pageSize: 500 },
  });

  const usernameById = (id: string): string => {
    if (id === "system") return "system";
    const u = (usersQuery.items ?? []).find((x: any) => x.id === id);
    return u?.username ?? id;
  };

  // Process and flatten the backup data
  const entries = (() => {
    const rawData = data?.items ?? [];
    const runs = rawData || [];
    const manual: any[] = []; // manual jobs if they exist in data structure
    const allJobs = [...manual];

    // Add jobs from runs
    runs.forEach((run: any) => {
      if (run.jobs) {
        allJobs.push(...run.jobs);
      } else {
        // If run doesn't have jobs, treat the run itself as a job
        allJobs.push(run);
      }
    });

    return allJobs.sort((a, b) => {
      const aTime = a.created_at ? new Date(a.created_at).getTime() : 0;
      const bTime = b.created_at ? new Date(b.created_at).getTime() : 0;
      return bTime - aTime;
    });
  })();

  // Filter entries based on current filters
  const filteredEntries = entries.filter((entry: BackupLogEntry) => {
    if (filters.from && entry.created_at && dayjs(entry.created_at).isBefore(filters.from)) return false;
    if (filters.to && entry.created_at && dayjs(entry.created_at).isAfter(filters.to)) return false;
    if (filters.kind && entry.kind && !entry.kind.includes(filters.kind)) return false;
    if (filters.status && entry.status !== filters.status) return false;
    if (filters.user_id && entry.user_id !== filters.user_id) return false;
    return true;
  });

  const paginatedEntries = filteredEntries.slice((page - 1) * pageSize, page * pageSize);

  const handleViewLog = async (entry: BackupLogEntry) => {
    setSelectedLog(entry);
    setLoadingDetails(true);
    setLogDetails(null);

    try {
      const endpoint = entry.user_id === "system"
        ? `/admin/system/backups/${entry.id}/logs`
        : `/admin/backups/${entry.id}/logs`;

      const response = await apiClient.get<{ data: BackupLogDetails }>(endpoint);
      setLogDetails(response.data.data);
    } catch (err) {
      console.error("Failed to fetch log details:", err);
      setLogDetails({
        unit: "unknown",
        status: "error",
        log_text: "Failed to fetch log details. The log may not be available.",
        fetched_at: new Date().toISOString(),
      });
    } finally {
      setLoadingDetails(false);
    }
  };

  const renderKindTag = (kind: string) => {
    const colorMap: Record<string, string> = {
      account_backup: "blue",
      account_restore: "green",
      system_backup: "purple",
      system_restore: "orange",
    };
    return <Tag color={colorMap[kind] || "default"}>{kind.replace("_", " ")}</Tag>;
  };

  const renderStatusTag = (status: string) => {
    const colorMap: Record<string, string> = {
      succeeded: "success",
      running: "processing",
      queued: "default",
      failed: "error",
      cancelled: "warning",
    };
    return <Tag color={colorMap[status] || "default"}>{status}</Tag>;
  };

  return (
    <div>
      <Space style={{ width: "100%", justifyContent: "space-between", marginBottom: 12, flexWrap: "wrap", rowGap: 8 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          Backup & Restore Logs
        </Typography.Title>
        <Space>
          <span
            role="button"
            onClick={() => refetch()}
            style={{ cursor: "pointer", color: "#1677ff", display: "inline-flex", alignItems: "center", gap: 4 }}
          >
            <ReloadOutlined spin={isFetching} /> Refresh
          </span>
        </Space>
      </Space>

      <Space wrap style={{ marginBottom: 12 }}>
        <DatePicker
          showTime
          placeholder="From"
          value={filters.from}
          onChange={(v) => setFilters((f) => ({ ...f, from: v }))}
        />
        <DatePicker
          showTime
          placeholder="To"
          value={filters.to}
          onChange={(v) => setFilters((f) => ({ ...f, to: v }))}
        />
        <Input
          placeholder="Kind contains"
          allowClear
          value={filters.kind ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, kind: e.target.value }))}
          style={{ width: 150 }}
        />
        <Input
          placeholder="Status"
          allowClear
          value={filters.status ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, status: e.target.value }))}
          style={{ width: 120 }}
        />
        <Input
          placeholder="User ID"
          allowClear
          value={filters.user_id ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, user_id: e.target.value }))}
          style={{ width: 150 }}
        />
      </Space>

      {error && (
        <Alert
          type="warning"
          message="Backup logs unavailable"
          description="The backup service isn't responding. Try again in a moment."
          style={{ marginBottom: 12 }}
          showIcon
        />
      )}

      <Table
        rowKey="id"
        dataSource={paginatedEntries}
        loading={isLoading}
        pagination={{
          current: page,
          pageSize,
          total: filteredEntries.length,
          showSizeChanger: false,
          onChange: (p) => setPage(p),
        }}
        scroll={{ x: "max-content" }}
        columns={[
          {
            title: "Job ID",
            dataIndex: "id",
            width: 120,
            render: (id: string) => (
              <Typography.Text code style={{ fontSize: "12px" }}>
                {id ? id.slice(0, 8) + "…" : "—"}
              </Typography.Text>
            ),
          },
          {
            title: "Kind",
            dataIndex: "kind",
            width: 140,
            render: renderKindTag,
          },
          {
            title: "Status",
            dataIndex: "status",
            width: 100,
            render: renderStatusTag,
          },
          {
            title: "User",
            dataIndex: "user_id",
            width: 120,
            render: usernameById,
          },
          {
            title: "Created",
            dataIndex: "created_at",
            width: 180,
            render: (v: string) => v ? new Date(v).toLocaleString() : "—",
          },
          {
            title: "Finished",
            dataIndex: "finished_at",
            width: 180,
            render: (v?: string) => v ? new Date(v).toLocaleString() : "—",
          },
          {
            title: "Actions",
            width: 100,
            render: (_: unknown, record: BackupLogEntry) => (
              <Button
                type="primary"
                size="small"
                icon={<FileTextOutlined />}
                onClick={() => handleViewLog(record)}
              >
                Logs
              </Button>
            ),
          },
        ]}
      />

      <Modal
        title={
          <Space>
            <FileTextOutlined />
            Backup Log Details
            {selectedLog && (
              <Tag color="blue">
                {selectedLog.kind?.replace("_", " ") || "unknown"} - {selectedLog.id ? selectedLog.id.slice(0, 8) + "…" : "no-id"}
              </Tag>
            )}
          </Space>
        }
        open={!!selectedLog}
        onCancel={() => {
          setSelectedLog(null);
          setLogDetails(null);
        }}
        footer={null}
        width="80%"
        style={{ top: 20 }}
      >
        {loadingDetails ? (
          <div style={{ textAlign: "center", padding: "40px 0" }}>
            <Spin size="large" />
            <Typography.Text style={{ display: "block", marginTop: 16 }}>
              Fetching log details...
            </Typography.Text>
          </div>
        ) : logDetails ? (
          <div>
            <Space style={{ marginBottom: 16 }}>
              <Typography.Text strong>Unit:</Typography.Text>
              <Typography.Text code>{logDetails.unit}</Typography.Text>
              <Typography.Text strong>Status:</Typography.Text>
              <Tag color={logDetails.status === "active" ? "green" : logDetails.status === "failed" ? "red" : "default"}>
                {logDetails.status}
              </Tag>
              {logDetails.exit_code !== undefined && (
                <>
                  <Typography.Text strong>Exit Code:</Typography.Text>
                  <Tag color={logDetails.exit_code === 0 ? "green" : "red"}>
                    {logDetails.exit_code}
                  </Tag>
                </>
              )}
              <Typography.Text strong>Fetched:</Typography.Text>
              <Typography.Text>{new Date(logDetails.fetched_at).toLocaleString()}</Typography.Text>
            </Space>
            <Typography.Title level={5}>Log Output:</Typography.Title>
            <pre
              style={{
                backgroundColor: "#f6f6f6",
                padding: "16px",
                borderRadius: "6px",
                fontSize: "13px",
                maxHeight: "400px",
                overflow: "auto",
                whiteSpace: "pre-wrap",
                wordBreak: "break-all",
              }}
            >
              {logDetails.log_text || "No log output available."}
            </pre>
          </div>
        ) : null}
      </Modal>
    </div>
  );
};