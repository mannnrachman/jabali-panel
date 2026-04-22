// Admin Dashboard — shows system info (CPU, RAM, disk, uptime) and
// service status from the agent via /api/v1/system/*.
//
// This is a read-only page: no mutations, no forms. The data is fetched
// once on mount; a manual refresh button lets operators re-poll without
// navigating away.
import { useEffect, useState, type ReactNode } from "react";
import {
  Card,
  Col,
  Descriptions,
  notification,
  Progress,
  Row,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  Button,
} from "antd";
import {
  ReloadOutlined,
  RedoOutlined,
  GlobalOutlined,
  DatabaseOutlined,
  ThunderboltOutlined,
  MailOutlined,
  InboxOutlined,
  SafetyOutlined,
  ApiOutlined,
  AppstoreOutlined,
  SettingOutlined,
  CodeOutlined,
  ClockCircleOutlined,
  PoweroffOutlined,
  CheckOutlined,
  CloseOutlined,
} from "@ant-design/icons";

import { apiClient } from "../../apiClient";

type PartitionInfo = {
  mount_point: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
};

type SystemInfo = {
  hostname: string;
  uptime_seconds: number;
  load_avg: [number, number, number];
  cpu_count: number;
  mem_total_kb: number;
  mem_available_kb: number;
  mem_used_kb: number;
  partitions: PartitionInfo[];
};

type ServiceStatus = {
  name: string;
  active: string;
  load_state: string; // "loaded" | "masked" | "not-found" | "error"
  enabled: string; // "enabled" | "disabled" | "static" | "masked" | ...
};

// serviceDisplay maps raw systemd unit names to UI presentation — label,
// one-line subtitle, and leading icon. The backend stays protocol-pure
// (raw systemctl output); visual mapping lives here so we can evolve it
// without touching the agent's security boundary.
const serviceDisplay: Record<
  string,
  { label: string; subtitle: string; icon: ReactNode }
> = {
  nginx: {
    label: "Nginx",
    subtitle: "Web Server",
    icon: <GlobalOutlined style={{ color: "#389e0d" }} />,
  },
  mariadb: {
    label: "MariaDB",
    subtitle: "Database Server",
    icon: <DatabaseOutlined style={{ color: "#389e0d" }} />,
  },
  "redis-server": {
    label: "Redis",
    subtitle: "Cache Server",
    icon: <ThunderboltOutlined style={{ color: "#389e0d" }} />,
  },
  "jabali-stalwart": {
    label: "Stalwart",
    subtitle: "Mail Server",
    icon: <MailOutlined style={{ color: "#389e0d" }} />,
  },
  "jabali-webmail": {
    label: "Bulwark",
    subtitle: "Webmail",
    icon: <InboxOutlined style={{ color: "#389e0d" }} />,
  },
  "jabali-kratos": {
    label: "Kratos",
    subtitle: "Identity Provider",
    icon: <SafetyOutlined style={{ color: "#389e0d" }} />,
  },
  pdns: {
    label: "PowerDNS",
    subtitle: "DNS Server",
    icon: <ApiOutlined style={{ color: "#389e0d" }} />,
  },
  "jabali-panel": {
    label: "Jabali Panel",
    subtitle: "Control Panel",
    icon: <AppstoreOutlined style={{ color: "#389e0d" }} />,
  },
  "jabali-agent": {
    label: "Jabali Agent",
    subtitle: "Panel Agent Daemon",
    icon: <SettingOutlined style={{ color: "#389e0d" }} />,
  },
  ssh: {
    label: "SSH",
    subtitle: "Secure Shell",
    icon: <CodeOutlined style={{ color: "#389e0d" }} />,
  },
  cron: {
    label: "Cron",
    subtitle: "Task Scheduler",
    icon: <ClockCircleOutlined style={{ color: "#389e0d" }} />,
  },
};

// Services the panel refuses to stop/disable because doing so locks the
// operator out of the UI. The API layer also enforces this; the UI
// hides the buttons so we don't round-trip to get a 409.
const selfDestructServices = new Set(["jabali-panel", "jabali-agent"]);

type ServicesResponse = {
  services: ServiceStatus[];
};

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h ${mins}m`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

function formatBytes(bytes: number): string {
  if (bytes >= 1e12) return `${(bytes / 1e12).toFixed(1)} TB`;
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
  if (bytes >= 1e6) return `${(bytes / 1e6).toFixed(1)} MB`;
  return `${(bytes / 1e3).toFixed(0)} KB`;
}

function isEnabled(enabled: string): boolean {
  // systemctl is-enabled output: "enabled", "enabled-runtime", "static",
  // and "generated" all count as "present and will start at boot" for
  // the operator's mental model. "alias" + "indirect" are dependency
  // hints; "disabled" is the clear negative.
  return (
    enabled === "enabled" ||
    enabled === "enabled-runtime" ||
    enabled === "static" ||
    enabled === "generated"
  );
}

function isRunning(active: string): boolean {
  return active === "active" || active === "activating";
}

export function Dashboard() {
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [services, setServices] = useState<ServiceStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // actingOn: which (name, verb) pair is in-flight. One-at-a-time so the
  // UI can show a spinner on the specific button without locking the
  // whole table.
  const [actingOn, setActingOn] = useState<{ name: string; verb: string } | null>(null);

  const fetchData = async () => {
    setLoading(true);
    setError(null);
    try {
      const [infoResp, svcResp] = await Promise.all([
        apiClient.get<SystemInfo>("/system/info"),
        apiClient.get<ServicesResponse>("/system/services"),
      ]);
      setInfo(infoResp.data);
      setServices(svcResp.data.services);
    } catch (err) {
      const detail =
        (err as { response?: { data?: { detail?: string } } }).response?.data
          ?.detail ?? "Could not reach agent";
      setError(detail);
    } finally {
      setLoading(false);
    }
  };

  // actOnService hits POST /system/services/:name/:verb and patches the
  // single row in place. Errors surface as a toast; the user can click
  // Refresh for a full re-fetch. verb ∈ restart | stop | start | enable | disable.
  const actOnService = async (name: string, verb: string) => {
    setActingOn({ name, verb });
    try {
      const resp = await apiClient.post<ServiceStatus>(
        `/system/services/${encodeURIComponent(name)}/${verb}`,
      );
      setServices((prev) =>
        prev.map((s) => (s.name === name ? { ...s, ...resp.data } : s)),
      );
      notification.success({
        message: `${verb.charAt(0).toUpperCase() + verb.slice(1)}ed ${name}`,
        description: `Status is now ${resp.data.active} / ${resp.data.enabled}.`,
      });
    } catch (err) {
      const detail =
        (err as { response?: { data?: { detail?: string } } }).response?.data
          ?.detail ?? `${verb} failed`;
      notification.error({
        message: `Failed to ${verb} ${name}`,
        description: detail,
      });
    } finally {
      setActingOn(null);
    }
  };

  useEffect(() => {
    fetchData();
  }, []);

  if (loading && !info) {
    return (
      <div style={{ textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }

  if (error && !info) {
    return (
      <div >
        <Typography.Text type="danger">{error}</Typography.Text>
      </div>
    );
  }

  const memPercent = info
    ? Math.round((info.mem_used_kb / info.mem_total_kb) * 100)
    : 0;

  return (
    <div >
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          Dashboard
        </Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={fetchData} loading={loading}>
          Refresh
        </Button>
      </Space>

      {info && (
        <>
          <Row gutter={[16, 16]}>
            <Col xs={24} md={12}>
              <Card title="System">
                <Descriptions column={1}>
                  <Descriptions.Item label="Hostname">
                    {info.hostname}
                  </Descriptions.Item>
                  <Descriptions.Item label="Uptime">
                    {formatUptime(info.uptime_seconds)}
                  </Descriptions.Item>
                  <Descriptions.Item label="Load average">
                    {info.load_avg.map((v) => v.toFixed(2)).join(", ")}
                  </Descriptions.Item>
                  <Descriptions.Item label="CPUs">
                    {info.cpu_count}
                  </Descriptions.Item>
                </Descriptions>
              </Card>
            </Col>
            <Col xs={24} md={12}>
              <Card title="Memory">
                <Progress
                  percent={memPercent}
                  status={memPercent > 90 ? "exception" : undefined}
                  format={() =>
                    `${formatBytes(info.mem_used_kb * 1024)} / ${formatBytes(info.mem_total_kb * 1024)}`
                  }
                />
              </Card>
            </Col>
          </Row>

          {info.partitions.length > 0 && (
            <Card title="Disk" style={{ marginTop: 16 }}>
              <Table
                dataSource={info.partitions}
                rowKey="mount_point"
                pagination={false}
                scroll={{ x: "max-content" }}
              >
                <Table.Column
                  dataIndex="mount_point"
                  title="Mount"
                />
                <Table.Column
                  dataIndex="total_bytes"
                  title="Total"
                  render={(v: number) => formatBytes(v)}
                />
                <Table.Column
                  dataIndex="used_bytes"
                  title="Used"
                  render={(v: number) => formatBytes(v)}
                />
                <Table.Column
                  dataIndex="free_bytes"
                  title="Free"
                  render={(v: number) => formatBytes(v)}
                />
                <Table.Column
                  title="Usage"
                  render={(_: unknown, r: PartitionInfo) => {
                    const pct = Math.round((r.used_bytes / r.total_bytes) * 100);
                    return (
                      <Progress
                        percent={pct}
                        status={pct > 90 ? "exception" : undefined}
                      />
                    );
                  }}
                />
              </Table>
            </Card>
          )}
        </>
      )}

      <Card title="Services" style={{ marginTop: 16 }}>
        <Table<ServiceStatus>
          dataSource={services}
          rowKey="name"
          pagination={false}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<ServiceStatus>
            title="Service"
            render={(_: unknown, row: ServiceStatus) => {
              const display = serviceDisplay[row.name] ?? {
                label: row.name,
                subtitle: "",
                icon: null,
              };
              return (
                <Space size="middle">
                  <div style={{ fontSize: 18, lineHeight: 1 }}>
                    {display.icon}
                  </div>
                  <div>
                    <div>{display.label}</div>
                    {display.subtitle && (
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {display.subtitle}
                      </Typography.Text>
                    )}
                  </div>
                </Space>
              );
            }}
          />
          <Table.Column<ServiceStatus>
            title="Status"
            width={120}
            align="center"
            render={(_: unknown, row: ServiceStatus) => (
              <Tag color={isRunning(row.active) ? "green" : row.active === "failed" ? "red" : "default"}>
                {isRunning(row.active) ? "Running" : row.active === "failed" ? "Failed" : "Stopped"}
              </Tag>
            )}
          />
          <Table.Column<ServiceStatus>
            title="Boot"
            width={120}
            align="center"
            render={(_: unknown, row: ServiceStatus) => (
              <Tag color={isEnabled(row.enabled) ? "green" : "orange"}>
                {isEnabled(row.enabled) ? "Enabled" : "Disabled"}
              </Tag>
            )}
          />
          <Table.Column<ServiceStatus>
            title=""
            width={320}
            align="right"
            render={(_: unknown, row: ServiceStatus) => {
              const busy = actingOn !== null;
              const hideSelfDestruct = selfDestructServices.has(row.name);
              const running = isRunning(row.active);
              const enabled = isEnabled(row.enabled);

              return (
                <Space size={4}>
                  {/* Stop (if running) — hidden for self-destruct services */}
                  {running && !hideSelfDestruct && (
                    <Button
                      type="text"
                      danger
                      size="small"
                      icon={<PoweroffOutlined />}
                      loading={actingOn?.name === row.name && actingOn?.verb === "stop"}
                      disabled={busy}
                      onClick={() => actOnService(row.name, "stop")}
                    >
                      Stop
                    </Button>
                  )}
                  {/* Start (if stopped) */}
                  {!running && (
                    <Button
                      type="text"
                      size="small"
                      icon={<CheckOutlined />}
                      loading={actingOn?.name === row.name && actingOn?.verb === "start"}
                      disabled={busy}
                      onClick={() => actOnService(row.name, "start")}
                    >
                      Start
                    </Button>
                  )}
                  {/* Restart (if running) */}
                  {running && (
                    <Button
                      type="text"
                      size="small"
                      icon={<RedoOutlined />}
                      loading={actingOn?.name === row.name && actingOn?.verb === "restart"}
                      disabled={busy}
                      onClick={() => actOnService(row.name, "restart")}
                    >
                      Restart
                    </Button>
                  )}
                  {/* Enable (if disabled) */}
                  {!enabled && (
                    <Button
                      type="text"
                      size="small"
                      icon={<CheckOutlined />}
                      loading={actingOn?.name === row.name && actingOn?.verb === "enable"}
                      disabled={busy}
                      onClick={() => actOnService(row.name, "enable")}
                    >
                      Enable
                    </Button>
                  )}
                  {/* Disable (if enabled) — hidden for self-destruct services */}
                  {enabled && !hideSelfDestruct && (
                    <Button
                      type="text"
                      size="small"
                      icon={<CloseOutlined />}
                      loading={actingOn?.name === row.name && actingOn?.verb === "disable"}
                      disabled={busy}
                      onClick={() => actOnService(row.name, "disable")}
                    >
                      Disable
                    </Button>
                  )}
                </Space>
              );
            }}
          />
        </Table>
      </Card>
    </div>
  );
}
