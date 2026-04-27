// Admin Dashboard — top-level summary. Deep host metrics live on
// /jabali-admin/server-status (M31, ADR-0065). The Dashboard is now a
// short landing card: hostname, top-level health roll-up, three count
// stats (users / domains / mailboxes) and a prominent button into the
// Server Status page.
import { Alert, Button, Card, Col, Row, Space, Tag, Typography } from "antd";
import type { ReactNode } from "react";
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

const formatCount = (n: number | undefined) =>
  n == null ? "—" : n.toLocaleString();

interface StatCardProps {
  label: string;
  value: number | undefined;
  icon: ReactNode;
  iconBg: string;
  iconColor: string;
  to: string;
}

const StatCard = ({ label, value, icon, iconBg, iconColor, to }: StatCardProps) => (
  <Link to={to} style={{ display: "block", color: "inherit" }}>
    <Card hoverable size="small" styles={{ body: { padding: 16 } }}>
      <Space size={16} align="center" style={{ width: "100%" }}>
        <div
          style={{
            width: 48,
            height: 48,
            borderRadius: 12,
            background: iconBg,
            color: iconColor,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            fontSize: 22,
            flex: "0 0 48px",
          }}
        >
          {icon}
        </div>
        <Space direction="vertical" size={2} style={{ minWidth: 0 }}>
          <Typography.Text
            type="secondary"
            style={{ fontSize: 12, letterSpacing: 0.6, textTransform: "uppercase" }}
          >
            {label}
          </Typography.Text>
          <Typography.Title level={3} style={{ margin: 0, lineHeight: 1.1 }}>
            {formatCount(value)}
          </Typography.Title>
        </Space>
      </Space>
    </Card>
  </Link>
);

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
          <StatCard
            label="Total Users"
            value={users.data}
            icon={<TeamOutlined />}
            iconBg="rgba(22, 119, 255, 0.12)"
            iconColor="#1677ff"
            to="/jabali-admin/users"
          />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard
            label="Active Domains"
            value={domains.data}
            icon={<GlobalOutlined />}
            iconBg="rgba(146, 84, 222, 0.14)"
            iconColor="#9254de"
            to="/jabali-admin/domains"
          />
        </Col>
        <Col xs={24} sm={8}>
          <StatCard
            label="Mailboxes"
            value={mailboxes.data}
            icon={<MailOutlined />}
            iconBg="rgba(250, 140, 22, 0.14)"
            iconColor="#fa8c16"
            to="/jabali-admin/domains"
          />
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
