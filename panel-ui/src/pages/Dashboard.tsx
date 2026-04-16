// Dashboard — Phase 8 placeholder. Shows who's logged in so it's obvious
// the auth round-trip worked end-to-end. Phase 9+ replaces this with a
// real admin home (user count, system stats, activity log, …).
import { useGetIdentity, useLogout } from "@refinedev/core";
import { Button, Card, Descriptions, Space, Typography, Tag } from "antd";

type Identity = {
  id: string;
  email: string;
  isAdmin: boolean;
};

export const DashboardPage = () => {
  const { data: identity, isLoading } = useGetIdentity<Identity>();
  const { mutate: logout, isPending: loggingOut } = useLogout();

  return (
    <div style={{ padding: 24, maxWidth: 720, margin: "0 auto" }}>
      <Space direction="vertical" size="large" style={{ width: "100%" }}>
        <Typography.Title level={2} style={{ margin: 0 }}>
          Dashboard
        </Typography.Title>

        <Card title="Who you are" loading={isLoading}>
          {identity ? (
            <Descriptions column={1} size="small">
              <Descriptions.Item label="User ID">
                <Typography.Text code>{identity.id}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="Email">
                {identity.email}
              </Descriptions.Item>
              <Descriptions.Item label="Role">
                {identity.isAdmin ? (
                  <Tag color="red">admin</Tag>
                ) : (
                  <Tag>user</Tag>
                )}
              </Descriptions.Item>
            </Descriptions>
          ) : null}
        </Card>

        <Button
          danger
          onClick={() => logout()}
          loading={loggingOut}
          style={{ alignSelf: "flex-start" }}
        >
          Sign out
        </Button>
      </Space>
    </div>
  );
};
