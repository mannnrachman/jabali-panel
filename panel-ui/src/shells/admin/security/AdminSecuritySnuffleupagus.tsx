// AdminSecuritySnuffleupagus — Security tab card for M41 Snuffleupagus.
// Wave A renders a stub indicating the module is not yet enabled.
// Wave D fills in mode toggle + incidents table + rule kill switch.
import { Alert, Card, Space, Tag, Typography } from "antd";

import { useSnuffleupagusStatus } from "../../../hooks/useSecuritySnuffleupagus";

const { Text } = Typography;

const MODE_COLOR: Record<string, string> = {
  off: "default",
  simulation: "warning",
  enforce: "success",
};

export function AdminSecuritySnuffleupagus() {
  const { data, isLoading, refetch } = useSnuffleupagusStatus();

  if (isLoading) {
    return (
      <Card title="Snuffleupagus" size="small">
        <Text type="secondary">Loading…</Text>
      </Card>
    );
  }

  if (!data?.enabled) {
    return (
      <Card title="Snuffleupagus" size="small" extra={<a onClick={() => void refetch()}>Refresh</a>}>
        <Alert
          type="warning"
          showIcon
          message="Snuffleupagus disabled"
          description="The PHP-hardening module is not active. Toggle it on once the per-PHP-version build pipeline has run (M41 Wave A)."
        />
      </Card>
    );
  }

  return (
    <Card
      title="Snuffleupagus"
      size="small"
      extra={
        <Space>
          <Tag color={MODE_COLOR[data.mode]}>{data.mode}</Tag>
          <a onClick={() => void refetch()}>Refresh</a>
        </Space>
      }
    >
      <Space direction="vertical" size="small" style={{ width: "100%" }}>
        <Text>
          PHP RCE hardening across {data.php_versions_loaded.length} installed PHP minor
          {data.php_versions_loaded.length === 1 ? "" : "s"}. Rules maintained by Jabali; mode set
          server-wide.
        </Text>
        {data.last_applied_at && (
          <Text type="secondary">
            Last apply: {new Date(data.last_applied_at).toLocaleString()}
          </Text>
        )}
        {/* Wave D: mode toggle + incidents table + rules kill switch */}
        <Alert
          type="info"
          showIcon
          message="UI surface lands in M41 Wave D"
          description="Mode toggle, recent incidents, and per-rule overrides will appear here."
        />
      </Space>
    </Card>
  );
}
