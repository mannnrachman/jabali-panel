import { Alert, Typography } from "antd";

import { DNSSECTable } from "../../../components/dnssec/DNSSECTable";

export const AdminDNSSECPage = () => (
  <div>
    <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
      DNSSEC
    </Typography.Title>
    <Alert
      type="info"
      showIcon
      style={{ marginBottom: 16 }}
      message="Sign zones with DNSSEC. Enable per-domain, then publish the DS record at the registrar."
      description="Signing is best-effort NSEC3 with ECDSAP256SHA256 (RFC 8624). Keys are managed by PowerDNS via pdnsutil."
    />
    <DNSSECTable showOwner />
  </div>
);
