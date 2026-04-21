import { Card, Space, Typography } from "antd";
import { SSLManagerTable } from "../../../components/ssl/SSLManagerTable";

export const UserSSLManagerPage = () => {
  return (
    <div >
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
        orientation="vertical"
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          SSL Certificates
        </Typography.Title>
        <Typography.Text type="secondary">
          Manage SSL for your domains.
        </Typography.Text>
      </Space>

      <Card>
        <SSLManagerTable endpoint="/ssl-certificates" showOwner={false} />
      </Card>
    </div>
  );
};
