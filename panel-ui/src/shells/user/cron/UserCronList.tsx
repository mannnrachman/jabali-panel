import { useMemo, useState } from "react";
import {
  App,
  Button,
  Card,
  Input,
  Popconfirm,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import {
  CalendarCheckOutlined,
  PlusSquareOutlined,
  DeleteOutlined,
  PlayCircleOutlined,
  EyeOutlined,
  CheckOutlined,
  CloseOutlined,
} from "@icons";
import { useQuery } from "@tanstack/react-query";
import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";
import {
  listCronJobs,
  deleteCronJob,
  updateCronJob,
  runCronJobNow,
  type CronJob,
} from "../../../apiClient";
import { CreateCronModal } from "./CreateCronModal";
import { CronLogDrawer } from "./CronLogDrawer";
import { RunNowResultModal } from "./RunNowResultModal";

dayjs.extend(relativeTime);

const SCHEDULE_PRESETS: Record<string, string> = {
  "0 * * * *": "Hourly",
  "0 3 * * *": "Daily at 3 AM",
  "0 3 * * 0": "Weekly (Sun 3 AM)",
  "0 3 1 * *": "Monthly (1st 3 AM)",
};

const humanizeSchedule = (schedule: string): string => {
  return SCHEDULE_PRESETS[schedule] || schedule;
};

export const UserCronList = () => {
  const { message: antMessage } = App.useApp();
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [editingJob, setEditingJob] = useState<CronJob | null>(null);
  const [logDrawerOpen, setLogDrawerOpen] = useState(false);
  const [logJobId, setLogJobId] = useState<string | null>(null);
  const [runNowModalOpen, setRunNowModalOpen] = useState(false);
  const [runNowResult, setRunNowResult] = useState<{
    exit_code: number;
    stdout: string;
    stderr: string;
  } | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [runningId, setRunningId] = useState<string | null>(null);
  const [togglingId, setTogglingId] = useState<string | null>(null);

  const {
    data: listResponse = { items: [] },
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ["cron-jobs"],
    queryFn: async () => listCronJobs(),
  });

  const jobs = listResponse.items || [];
  const [search, setSearch] = useState("");
  const filteredJobs = useMemo(() => {
    if (!search) return jobs;
    const needle = search.toLowerCase();
    return jobs.filter(
      (j) =>
        j.name.toLowerCase().includes(needle) ||
        j.command.toLowerCase().includes(needle) ||
        j.schedule.toLowerCase().includes(needle),
    );
  }, [jobs, search]);

  const handleOpenCreateModal = () => {
    setEditingJob(null);
    setCreateModalOpen(true);
  };

  const handleOpenEditModal = (job: CronJob) => {
    setEditingJob(job);
    setCreateModalOpen(true);
  };

  const handleCreateSuccess = () => {
    setCreateModalOpen(false);
    setEditingJob(null);
    refetch();
  };

  const handleDelete = async (job: CronJob) => {
    setDeletingId(job.id);
    try {
      await deleteCronJob(job.id);
      antMessage.success("Cron job deleted successfully");
      refetch();
    } catch (error) {
      const msg =
        (error as { response?: { data?: { detail?: string } }; message?: string })
          ?.response?.data?.detail ??
        (error as { message?: string })?.message ??
        "Failed to delete cron job";
      antMessage.error(msg);
    } finally {
      setDeletingId(null);
    }
  };

  const handleRunNow = async (job: CronJob) => {
    setRunningId(job.id);
    try {
      const result = await runCronJobNow(job.id);
      setRunNowResult(result);
      setRunNowModalOpen(true);
      // Refetch to update last_run_at and last_exit_code
      setTimeout(() => refetch(), 2000);
    } catch (error) {
      const msg =
        (error as { response?: { data?: { detail?: string } }; message?: string })
          ?.response?.data?.detail ??
        (error as { message?: string })?.message ??
        "Failed to run cron job";
      antMessage.error(msg);
    } finally {
      setRunningId(null);
    }
  };

  const handleToggleEnabled = async (job: CronJob) => {
    setTogglingId(job.id);
    try {
      await updateCronJob(job.id, { enabled: !job.enabled });
      antMessage.success(
        job.enabled ? "Cron job disabled" : "Cron job enabled"
      );
      refetch();
    } catch (error) {
      const msg =
        (error as { response?: { data?: { detail?: string } }; message?: string })
          ?.response?.data?.detail ??
        (error as { message?: string })?.message ??
        "Failed to update cron job";
      antMessage.error(msg);
    } finally {
      setTogglingId(null);
    }
  };

  const truncateCommand = (cmd: string): string => {
    if (cmd.length <= 40) return cmd;
    return cmd.substring(0, 40) + "…";
  };

  const isLoading_ = isLoading || deletingId !== null;

  return (
    <div>
      <Space
        wrap
        align="center"
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          <CalendarCheckOutlined /> Cron Jobs
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={handleOpenCreateModal}
        >
          New Cron Job
        </Button>
      </Space>

      <Card>
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <Input.Search
          placeholder="Search by name, command, or schedule"
          allowClear
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          onSearch={(value) => setSearch(value.trim())}
          style={{ maxWidth: 360 }}
        />
        <Table<CronJob>
        dataSource={filteredJobs}
        loading={isLoading_}
        rowKey="id"
        pagination={false}
        scroll={{ x: "max-content" }}
        columns={[
          {
            title: "Name",
            dataIndex: "name",
            sorter: (a, b) => a.name.localeCompare(b.name),
            defaultSortOrder: "ascend",
          },
          {
            title: "Schedule",
            dataIndex: "schedule",
            render: (schedule: string) => (
              <Tooltip title={schedule}>
                <span>{humanizeSchedule(schedule)}</span>
              </Tooltip>
            ),
          },
          {
            title: "Command",
            dataIndex: "command",
            render: (command: string) => (
              <Tooltip title={command}>
                <span
                  style={{
                    fontFamily: "monospace",
                  }}
                >
                  {truncateCommand(command)}
                </span>
              </Tooltip>
            ),
          },
          {
            title: "Last Run",
            dataIndex: "last_run_at",
            render: (lastRunAt: string | null) =>
              lastRunAt ? dayjs(lastRunAt).fromNow() : "Never",
          },
          {
            title: "Last Exit",
            dataIndex: "last_exit_code",
            render: (code: number | null) => {
              if (code === null) return <Typography.Text type="secondary">—</Typography.Text>;
              if (code === 0)
                return <Tag color="green">{code}</Tag>;
              return <Tag color="red"><code>{code}</code></Tag>;
            },
          },
          {
            title: "Enabled",
            dataIndex: "enabled",
            render: (enabled: boolean, record) => (
              <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />}
                checked={enabled}
                onChange={() => handleToggleEnabled(record)}
                loading={togglingId === record.id}
                disabled={togglingId !== null && togglingId !== record.id}
              />
            ),
          },
          {
            title: "Actions",
            dataIndex: "actions",
            render: (_, record) => (
              <Space>
                <Button
                  type="text"
                  icon={<PlayCircleOutlined />}
                  onClick={() => handleRunNow(record)}
                  loading={runningId === record.id}
                  disabled={
                    runningId !== null && runningId !== record.id
                  }
                >
                  Run now
                </Button>
                <Button
                  type="text"
                  icon={<EyeOutlined />}
                  onClick={() => {
                    setLogJobId(record.id);
                    setLogDrawerOpen(true);
                  }}
                >
                  Log
                </Button>
                <Button
                  type="text"
                  onClick={() => handleOpenEditModal(record)}
                >
                  Edit
                </Button>
                <Popconfirm
                  title="Delete Cron Job"
                  description="Are you sure you want to delete this cron job?"
                  onConfirm={() => handleDelete(record)}
                  okText="Yes"
                  cancelText="No"
                >
                  <Button
                    type="text"
                    danger
                    icon={<DeleteOutlined />}
                    loading={deletingId === record.id}
                    disabled={deletingId !== null && deletingId !== record.id}
                  >
                    Delete
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
        />
        </Space>
      </Card>

      <CreateCronModal
        open={createModalOpen}
        onClose={() => {
          setCreateModalOpen(false);
          setEditingJob(null);
        }}
        onSuccess={handleCreateSuccess}
        initial={editingJob}
      />

      {logJobId && (
        <CronLogDrawer
          open={logDrawerOpen}
          onClose={() => {
            setLogDrawerOpen(false);
            setLogJobId(null);
          }}
          jobId={logJobId}
        />
      )}

      <RunNowResultModal
        open={runNowModalOpen}
        onClose={() => {
          setRunNowModalOpen(false);
          setRunNowResult(null);
        }}
        result={runNowResult}
      />
    </div>
  );
};
