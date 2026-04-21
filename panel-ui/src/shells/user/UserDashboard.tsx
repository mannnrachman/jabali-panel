// UserDashboard — the tenant landing view.
//
// Surfaces the "where am I and how is it going?" information: who I'm
// signed in as and what resource headroom I've got left. Deeper work
// (domains, files, databases) lives behind their own sidebar links —
// the dashboard is intentionally just a glanceable summary.
import { Card, Col, Row, Space, Typography } from "antd";
import { useEffect, useState } from "react";

import { getIdentity, type Identity } from "../../identity";
import { MyProfileUsageCard } from "./MyProfileUsageCard";

export function UserDashboard() {
  const [me, setMe] = useState<Identity | null>(null);

  useEffect(() => {
    getIdentity().then(setMe);
  }, []);

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Typography.Title level={3} style={{ margin: 0 }}>
        Dashboard
      </Typography.Title>

      <Row gutter={[16, 16]}>
        <Col xs={24} md={12}>
          <Card title="Account" loading={!me}>
            {me && (
              <Space direction="vertical" size={4}>
                <Typography.Text>
                  Signed in as <Typography.Text strong>{me.email}</Typography.Text>
                </Typography.Text>
                <Typography.Text type="secondary">
                  <Typography.Text code>{me.id}</Typography.Text>
                </Typography.Text>
              </Space>
            )}
          </Card>
        </Col>
        <Col xs={24} md={12}>
          {me ? (
            <MyProfileUsageCard userId={me.id} />
          ) : (
            <Card title="Resource usage" loading />
          )}
        </Col>
      </Row>
    </Space>
  );
}
