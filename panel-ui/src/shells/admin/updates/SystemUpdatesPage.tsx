// SystemUpdatesPage — admin self-update page (M29).
//
// Two stacked cards:
//   1. Jabali Panel: Check for updates → if behind, "Update Jabali panel"
//      button kicks off `system.update_run`. Status + log tail polled
//      every 2 s while the unit is active.
//   2. System Packages: Check for updates → runs apt-get update + parses
//      apt list --upgradable. Renders a 3-col table; "Apply updates"
//      button starts dist-upgrade as a transient unit.
import { useState } from "react";
import {
  Alert,
  Button,
  Card,
  Empty,
  Space,
  Table,
  Typography,
  message,
} from "antd";

import { DownloadOutlined, ReloadOutlined } from "@icons";

import { JobLogTail } from "../../../components/JobLogTail";
import {
  useAptCheck,
  useAptRun,
  useAptStatus,
  useAptStop,
  useJabaliCheck,
  useJabaliRun,
  useJabaliStatus,
  useJabaliStop,
  type AptPackage,
} from "../../../hooks/useSystemUpdates";

export const SystemUpdatesPage = () => (
  <div>
    <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
      Updates
    </Typography.Title>
    <Space direction="vertical" size={16} style={{ width: "100%" }}>
      <JabaliUpdateCard />
      <AptUpdateCard />
    </Space>
  </div>
);

function JabaliUpdateCard() {
  const [since, setSince] = useState<string | null>(null);
  const check = useJabaliCheck();
  const run = useJabaliRun();
  const stop = useJabaliStop();
  const status = useJabaliStatus(since);

  const result = check.data;
  const running =
    status.data?.status === "active" || status.data?.status === "activating";

  const onCheck = async () => {
    try {
      await check.mutateAsync();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "check failed");
    }
  };

  const onRun = async () => {
    try {
      const r = await run.mutateAsync();
      setSince(r.started_at);
      message.success("Update started");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "run failed");
    }
  };

  const onStop = async () => {
    try {
      await stop.mutateAsync();
      message.success("Stop signal sent");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "stop failed");
    }
  };

  return (
    <Card
      title="Jabali Panel"
      extra={
        <Button
          icon={<ReloadOutlined />}
          onClick={onCheck}
          loading={check.isPending}
        >
          Check for updates
        </Button>
      }
    >
      {!result && !running ? (
        <Typography.Text type="secondary">
          Click "Check for updates" to compare your installation against the
          latest release on origin/main.
        </Typography.Text>
      ) : null}

      {result && result.behind_count === 0 && !running ? (
        <Alert
          type="success"
          showIcon
          message="Up to date"
          description={
            <span>
              Current commit{" "}
              <Typography.Text code>{result.current_sha.substring(0, 12)}</Typography.Text>{" "}
              matches origin/main.
            </span>
          }
        />
      ) : null}

      {result && result.behind_count > 0 && !running ? (
        <Alert
          type="warning"
          showIcon
          message={`${result.behind_count} commit${result.behind_count === 1 ? "" : "s"} behind`}
          description={
            <Space direction="vertical">
              <span>
                Local <Typography.Text code>{result.current_sha.substring(0, 12)}</Typography.Text>{" "}
                → remote <Typography.Text code>{result.remote_sha.substring(0, 12)}</Typography.Text>.
              </span>
              <Button
                type="primary"
                icon={<DownloadOutlined />}
                onClick={onRun}
                loading={run.isPending}
              >
                Update Jabali panel
              </Button>
            </Space>
          }
        />
      ) : null}

      {running ? (
        <Alert
          type="info"
          showIcon
          message="Update in progress"
          description={
            <Space>
              <Button danger size="small" loading={stop.isPending} onClick={onStop}>
                Stop
              </Button>
            </Space>
          }
        />
      ) : null}

      {since && status.data ? (
        <JobLogTail
          status={status.data.status}
          logTail={status.data.log_tail}
          exitCode={status.data.exit_code}
        />
      ) : null}
    </Card>
  );
}

function AptUpdateCard() {
  const [since, setSince] = useState<string | null>(null);
  const check = useAptCheck();
  const run = useAptRun();
  const stop = useAptStop();
  const status = useAptStatus(since);

  const result = check.data;
  const running =
    status.data?.status === "active" || status.data?.status === "activating";

  const onCheck = async () => {
    try {
      await check.mutateAsync();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "check failed");
    }
  };

  const onRun = async () => {
    try {
      const r = await run.mutateAsync();
      setSince(r.started_at);
      message.success("Apt upgrade started");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "run failed");
    }
  };

  const onStop = async () => {
    try {
      await stop.mutateAsync();
      message.success("Stop signal sent");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "stop failed");
    }
  };

  return (
    <Card
      title="System Packages"
      extra={
        <Button
          icon={<ReloadOutlined />}
          onClick={onCheck}
          loading={check.isPending}
        >
          Check for updates
        </Button>
      }
    >
      {!result && !running ? (
        <Typography.Text type="secondary">
          Click "Check for updates" to run <code>apt-get update</code> and list
          all upgradable system packages.
        </Typography.Text>
      ) : null}

      {result && result.total === 0 && !running ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="System is up to date"
        />
      ) : null}

      {result && result.total > 0 && !running ? (
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            type="warning"
            showIcon
            message={`${result.total} package${result.total === 1 ? "" : "s"} can be upgraded`}
            description="dist-upgrade may pull in libc / openssh / mariadb. Take a snapshot first."
          />
          <Table<AptPackage>
            rowKey="name"
            size="small"
            dataSource={result.packages}
            pagination={false}
            scroll={{ x: "max-content" }}
            columns={[
              { title: "Package", dataIndex: "name" },
              { title: "Current", dataIndex: "current" },
              { title: "New", dataIndex: "new" },
            ]}
          />
          <Button
            type="primary"
            icon={<DownloadOutlined />}
            onClick={onRun}
            loading={run.isPending}
          >
            Apply updates
          </Button>
        </Space>
      ) : null}

      {running ? (
        <Alert
          type="info"
          showIcon
          message="Apt upgrade in progress"
          description={
            <Button danger size="small" loading={stop.isPending} onClick={onStop}>
              Stop
            </Button>
          }
        />
      ) : null}

      {since && status.data ? (
        <JobLogTail
          status={status.data.status}
          logTail={status.data.log_tail}
          exitCode={status.data.exit_code}
        />
      ) : null}
    </Card>
  );
}
