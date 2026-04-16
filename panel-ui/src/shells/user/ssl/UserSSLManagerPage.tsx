import { Card, Space, Typography } from "antd";
import { SSLManagerTable } from "../../../components/ssl/SSLManagerTable";

export const UserSSLManagerPage = () => {
  return (
    <div style={{ padding: 24 }}>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
        direction="vertical"
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          SSL Certificates
        </Typography.Title>
        <Typography.Text type="secondary">
          Manage SSL for your domains.
        </Typography.Text>
      </Space>

      <Card>
        <SSLManagerTable endpoint="/api/v1/ssl-certificates" showOwner={false} />
      </Card>
    </div>
  );
};
