import { Card, Typography } from "antd";
import { ShieldCheckOutlined } from "@icons";

import { SSLManagerTable } from "../../../components/ssl/SSLManagerTable";

export const SSLManagerPage = () => {
  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        <ShieldCheckOutlined /> SSL Manager
      </Typography.Title>

      <Card>
        <SSLManagerTable
          endpoint="/admin/ssl-certificates"
          showOwner={true}
        />
      </Card>
    </div>
  );
};
