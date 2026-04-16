// Admin Dashboard — shows system info (CPU, RAM, disk, uptime) and
// service status from the agent via /api/v1/system/*.
//
// This is a read-only page: no mutations, no forms. The data is fetched
// once on mount; a manual refresh button lets operators re-poll without
// navigating away.
import { useEffect, useState } from "react";
import {
  Card,
  Col,
  Descriptions,
  Progress,
  Row,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
  Button,
} from "antd";
import { ReloadOutlined } from "@ant-design/icons";

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
};

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

function serviceTagColor(active: string): string {
  switch (active) {
    case "active":
      return "green";
    case "inactive":
      return "default";
    case "failed":
      return "red";
    case "activating":
    case "deactivating":
      return "orange";
    default:
      return "default";
  }
}

export function Dashboard() {
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [services, setServices] = useState<ServiceStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

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

  useEffect(() => {
    fetchData();
  }, []);

  if (loading && !info) {
    return (
      <div style={{ padding: 24, textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }

  if (error && !info) {
    return (
      <div style={{ padding: 24 }}>
        <Typography.Text type="danger">{error}</Typography.Text>
      </div>
    );
  }

  const memPercent = info
    ? Math.round((info.mem_used_kb / info.mem_total_kb) * 100)
    : 0;

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
              <Card title="System" size="small">
                <Descriptions column={1} size="small">
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
              <Card title="Memory" size="small">
                <Progress
                  percent={memPercent}
                  status={memPercent > 90 ? "exception" : "active"}
                  format={() =>
                    `${formatBytes(info.mem_used_kb * 1024)} / ${formatBytes(info.mem_total_kb * 1024)}`
                  }
                />
              </Card>
            </Col>
          </Row>

          {info.partitions.length > 0 && (
            <Card title="Disk" size="small" style={{ marginTop: 16 }}>
              <Table
                dataSource={info.partitions}
                rowKey="mount_point"
                size="small"
                pagination={false}
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
                        size="small"
                        status={pct > 90 ? "exception" : "normal"}
                      />
                    );
                  }}
                />
              </Table>
            </Card>
          )}
        </>
      )}

      <Card title="Services" size="small" style={{ marginTop: 16 }}>
        <Table
          dataSource={services}
          rowKey="name"
          size="small"
          pagination={false}
        >
          <Table.Column dataIndex="name" title="Service" />
          <Table.Column
            dataIndex="active"
            title="Status"
            render={(active: string) => (
              <Tag color={serviceTagColor(active)}>{active}</Tag>
            )}
          />
        </Table>
      </Card>
    </div>
  );
}
