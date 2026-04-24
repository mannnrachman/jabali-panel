import { Alert, Typography } from "antd";

import { DNSSECTable } from "../../../components/dnssec/DNSSECTable";

export const UserDNSSECPage = () => (
  <div>
    <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
      DNSSEC
    </Typography.Title>
    <Alert
      type="info"
      showIcon
      style={{ marginBottom: 16 }}
      message="Protect your domain with DNSSEC."
      description="Enable signing here, then copy the DS record to your registrar to complete the chain of trust."
    />
    <DNSSECTable showOwner={false} />
  </div>
);
