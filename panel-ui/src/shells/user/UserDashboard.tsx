// UserDashboard — the tenant landing view.
//
// Two-column Masonry layout: account / usage on one side, four
// recent-rows tables (domains, mailboxes, applications, databases)
// on the other. Cards pack height-balanced like the admin shell so a
// long table doesn't leave whitespace next to a short one.
import {
  Button,
  Card,
  Col,
  Masonry,
  Row,
  Space,
  Table,
  Typography,
} from "antd";
import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useQueries } from "@tanstack/react-query";

import {
  GlobalOutlined,
  MailOutlined,
  AppstoreOutlined,
  DatabaseOutlined,
} from "@icons";

import { apiClient } from "../../apiClient";
import { useListQuery } from "../../hooks/useQueries";
import type { Mailbox } from "../../hooks/useMailboxes";
import { getIdentity, type Identity } from "../../identity";
import { MyProfileUsageCard } from "./MyProfileUsageCard";

const RECENT_LIMIT = 5;

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

type DomainRow = {
  id: string;
  name: string;
  email_enabled?: boolean;
  created_at?: string;
};
type ApplicationRow = {
  id: string;
  app_type?: string;
  domain_name?: string;
  status?: string;
  version?: string | null;
};
type DatabaseRow = {
  id: string;
  name: string;
  created_at?: string;
};
type MailboxRow = Mailbox & { domain_name: string };

export function UserDashboard() {
  const [me, setMe] = useState<Identity | null>(null);

  useEffect(() => {
    getIdentity().then(setMe);
  }, []);

  const recentDomains = useListQuery<DomainRow>({
    resource: "domains",
    params: { pageSize: RECENT_LIMIT, sort: "created_at", order: "desc" },
  });
  const allDomainsForMail = useListQuery<DomainRow>({
    resource: "domains",
    params: { pageSize: 200, sort: "name", order: "asc" },
  });
  const emailDomains = useMemo(
    () => allDomainsForMail.items.filter((d) => d.email_enabled),
    [allDomainsForMail.items],
  );
  const mailboxResults = useQueries({
    queries: emailDomains.map((d) => ({
      queryKey: ["dashboard", "mailboxes", d.id],
      queryFn: async () => {
        const { data } = await apiClient.get<{ data: Mailbox[]; total: number }>(
          `/domains/${d.id}/mailboxes?page=1&page_size=${RECENT_LIMIT}&sort=created_at&order=desc`,
        );
        return { items: data.data ?? [], total: data.total ?? 0, domain: d };
      },
    })),
  });
  const recentMailboxes: MailboxRow[] = useMemo(() => {
    const out: MailboxRow[] = [];
    for (const r of mailboxResults) {
      if (!r.data) continue;
      for (const mb of r.data.items) {
        out.push({ ...mb, domain_name: r.data.domain.name });
      }
    }
    return out
      .sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""))
      .slice(0, RECENT_LIMIT);
  }, [mailboxResults]);
  const mailboxesLoading = mailboxResults.some((r) => r.isLoading);
  const mailboxTotal = useMemo(() => {
    let n = 0;
    for (const r of mailboxResults) {
      if (r.data) n += r.data.total;
    }
    return n;
  }, [mailboxResults]);

  const recentApps = useListQuery<ApplicationRow>({
    resource: "applications",
    params: { pageSize: RECENT_LIMIT, sort: "created_at", order: "desc" },
  });
  const recentDatabases = useListQuery<DatabaseRow>({
    resource: "databases",
    params: { pageSize: RECENT_LIMIT, sort: "created_at", order: "desc" },
  });

  const items = [
    {
      key: "usage",
      data: null,
      children: me ? (
        <MyProfileUsageCard userId={me.id} />
      ) : (
        <Card title="Resource usage" loading />
      ),
    },
    {
      key: "domains",
      data: null,
      children: (
        <Card
          title="Recent Domains"
          size="small"
          extra={
            <Link to="/jabali-panel/domains">
              <Button type="primary" size="small">
                View all
              </Button>
            </Link>
          }
        >
          <Table<DomainRow>
            size="small"
            rowKey="id"
            pagination={false}
            loading={recentDomains.isLoading}
            dataSource={recentDomains.items}
            locale={{ emptyText: "No domains yet" }}
            columns={[{ title: "Domain", dataIndex: "name", ellipsis: true }]}
          />
        </Card>
      ),
    },
    {
      key: "mailboxes",
      data: null,
      children: (
        <Card
          title="Recent Mailboxes"
          size="small"
          extra={
            <Link to="/jabali-panel/mail/mailboxes">
              <Button type="primary" size="small">
                View all
              </Button>
            </Link>
          }
        >
          <Table<MailboxRow>
            size="small"
            rowKey="id"
            pagination={false}
            loading={allDomainsForMail.isLoading || mailboxesLoading}
            dataSource={recentMailboxes}
            locale={{ emptyText: "No mailboxes yet" }}
            columns={[
              {
                title: "Address",
                dataIndex: "email",
                ellipsis: true,
                render: (_v, r) => `${r.local_part}@${r.domain_name}`,
              },
            ]}
          />
        </Card>
      ),
    },
    {
      key: "apps",
      data: null,
      children: (
        <Card
          title="Recent Applications"
          size="small"
          extra={
            <Link to="/jabali-panel/applications">
              <Button type="primary" size="small">
                View all
              </Button>
            </Link>
          }
        >
          <Table<ApplicationRow>
            size="small"
            rowKey="id"
            pagination={false}
            loading={recentApps.isLoading}
            dataSource={recentApps.items}
            locale={{ emptyText: "No applications yet" }}
            columns={[
              {
                title: "Domain",
                dataIndex: "domain_name",
                ellipsis: true,
                render: (v: string | undefined) => v ?? "—",
              },
              {
                title: "Type",
                dataIndex: "app_type",
                width: 110,
                render: (v: string | undefined) => v ?? "—",
              },
            ]}
          />
        </Card>
      ),
    },
    {
      key: "databases",
      data: null,
      children: (
        <Card
          title="Recent Databases"
          size="small"
          extra={
            <Link to="/jabali-panel/databases">
              <Button type="primary" size="small">
                View all
              </Button>
            </Link>
          }
        >
          <Table<DatabaseRow>
            size="small"
            rowKey="id"
            pagination={false}
            loading={recentDatabases.isLoading}
            dataSource={recentDatabases.items}
            locale={{ emptyText: "No databases yet" }}
            columns={[
              { title: "Name", dataIndex: "name", ellipsis: true },
            ]}
          />
        </Card>
      ),
    },
  ];

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} md={6}>
          <StatCard
            label="Domains"
            value={recentDomains.total}
            icon={<GlobalOutlined />}
            iconBg="rgba(146, 84, 222, 0.14)"
            iconColor="#9254de"
            to="/jabali-panel/domains"
          />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard
            label="Mailboxes"
            value={mailboxTotal}
            icon={<MailOutlined />}
            iconBg="rgba(250, 140, 22, 0.14)"
            iconColor="#fa8c16"
            to="/jabali-panel/mail/mailboxes"
          />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard
            label="Applications"
            value={recentApps.total}
            icon={<AppstoreOutlined />}
            iconBg="rgba(22, 119, 255, 0.12)"
            iconColor="#1677ff"
            to="/jabali-panel/applications"
          />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard
            label="Databases"
            value={recentDatabases.total}
            icon={<DatabaseOutlined />}
            iconBg="rgba(82, 196, 26, 0.14)"
            iconColor="#52c41a"
            to="/jabali-panel/databases"
          />
        </Col>
      </Row>

      <Masonry
        columns={{ xs: 1, sm: 1, md: 1, lg: 2 }}
        gutter={16}
        items={items}
      />
    </Space>
  );
}
