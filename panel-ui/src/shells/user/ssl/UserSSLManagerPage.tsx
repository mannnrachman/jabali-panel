import { Card, Typography } from "antd";
import { SSLManagerTable } from "../../../components/ssl/SSLManagerTable";

export const UserSSLManagerPage = () => {
  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        SSL Certificates
      </Typography.Title>

      <Card>
        <SSLManagerTable endpoint="/ssl-certificates" showOwner={false} />
      </Card>
    </div>
  );
};
