// Admin Dashboard — top-level summary. Deep host metrics live on
// /jabali-admin/server-status (M31, ADR-0065). The Dashboard is now a
// short landing card: hostname, top-level health roll-up, three count
// stats (users / domains / mailboxes) and a prominent button into the
// Server Status page.
import { Alert, Button, Card, Col, Row, Space, Statistic, Tag, Typography } from "antd";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";

import { ServerOutlined, TeamOutlined, GlobalOutlined, MailOutlined } from "@icons";

import { apiClient } from "../../apiClient";
import { useServerStatus } from "../../hooks/useServerStatus";

interface CountsResponse {
  users: number;
  domains: number;
  mailboxes: number;
}

export const Dashboard = () => {
  const status = useServerStatus();
  const env = status.data;

  const counts = useQuery({
    queryKey: ["dashboard", "admin-counts"],
    queryFn: async () => {
      try {
        const r = await apiClient.get<CountsResponse>("/admin/counts");
        return r.data;
      } catch {
        return { users: 0, domains: 0, mailboxes: 0 } as CountsResponse;
      }
    },
  });
  const users = { data: counts.data?.users };
  const domains = { data: counts.data?.domains };
  const mailboxes = { data: counts.data?.mailboxes };

  const alerts = env?.alerts ?? [];
  const critical = alerts.filter((a) => a.level === "critical").length;
  const warnings = alerts.filter((a) => a.level === "warning").length;

  let healthTag = <Tag color="green">Healthy</Tag>;
  if (critical > 0) healthTag = <Tag color="red">{critical} critical</Tag>;
  else if (warnings > 0) healthTag = <Tag color="orange">{warnings} warning{warnings === 1 ? "" : "s"}</Tag>;

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        Dashboard
      </Typography.Title>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={8}>
          <Card size="small">
            <Statistic
              title="Total Users"
              value={users.data ?? 0}
              prefix={<TeamOutlined />}
            />
            <Link to="/jabali-admin/users">Manage →</Link>
          </Card>
        </Col>
        <Col xs={24} sm={8}>
          <Card size="small">
            <Statistic
              title="Active Domains"
              value={domains.data ?? 0}
              prefix={<GlobalOutlined />}
            />
            <Link to="/jabali-admin/domains">Manage →</Link>
          </Card>
        </Col>
        <Col xs={24} sm={8}>
          <Card size="small">
            <Statistic
              title="Mailboxes"
              value={mailboxes.data ?? 0}
              prefix={<MailOutlined />}
            />
            <Link to="/jabali-admin/domains">Manage →</Link>
          </Card>
        </Col>
      </Row>

      <Card style={{ marginBottom: 16 }}>
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Space size={12} wrap>
            <Typography.Title level={4} style={{ margin: 0 }}>
              {env?.host?.hostname ?? "—"}
            </Typography.Title>
            {healthTag}
          </Space>
          <Typography.Text type="secondary">
            Top-level summary. For live metrics, services, network, and processes,
            see Server Status.
          </Typography.Text>
          <Link to="/jabali-admin/server-status">
            <Button type="primary" icon={<ServerOutlined />}>
              View server status →
            </Button>
          </Link>
        </Space>
      </Card>

      {critical > 0 && (
        <Alert
          type="error"
          showIcon
          message={`${critical} critical issue${critical === 1 ? "" : "s"} on host`}
          description={
            <Link to="/jabali-admin/server-status">Open Server Status to investigate →</Link>
          }
          style={{ marginBottom: 16 }}
        />
      )}
    </div>
  );
};
