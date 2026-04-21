// MyProfile — user-panel profile page.
//
// Post-M20 auth lives in Kratos. Password changes and 2FA enrolment happen
// via Kratos's self-service settings flow at /.ory/self-service/settings/browser,
// not through panel-api — the panel DB is no longer an identity store.
import { Button, Card, Descriptions, Space, Typography } from "antd";
import { useEffect, useState } from "react";

import { getIdentity, type Identity } from "../../identity";
import { MyProfileUsageCard } from "./MyProfileUsageCard";

export function MyProfile() {
  const [me, setMe] = useState<Identity | null>(null);

  useEffect(() => {
    getIdentity().then(setMe);
  }, []);

  return (
    <div style={{ maxWidth: 720, margin: "0 auto" }}>
      <Space orientation="vertical" size="large" style={{ width: "100%" }}>
        <Typography.Title level={2} style={{ margin: 0 }}>
          My profile
        </Typography.Title>

        <Card title="Account" loading={!me}>
          {me && (
            <Descriptions column={1}>
              <Descriptions.Item label="Email">{me.email}</Descriptions.Item>
              <Descriptions.Item label="User ID">
                <Typography.Text code>{me.id}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>
          )}
        </Card>

        <Card title="Security">
          <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>
            Password changes and two-factor authentication are managed by our
            identity provider. You'll be redirected there and returned here
            when you're done.
          </Typography.Paragraph>
          <Button
            type="primary"
            href="/.ory/self-service/settings/browser"
          >
            Manage account security
          </Button>
        </Card>

        {me && <MyProfileUsageCard userId={me.id} />}
      </Space>
    </div>
  );
}
