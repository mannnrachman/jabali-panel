// AdminSecurityModsec — M26 Step 8. Three cards: global engine + paranoia,
// per-domain toggles (paginated), audit-log tail (auto-refresh 30s).
import {
  Alert,
  Button,
  Card,
  Empty,
  message,
  Popconfirm,
  Slider,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from "antd";
import { useState } from "react";

import {
  useModsecAudit,
  useModsecDomains,
  useModsecGlobal,
  useUpdateModsecDomain,
  useUpdateModsecGlobal,
  type ModsecAuditEntry,
  type ModsecDomainRow,
  type ModsecEngineMode,
} from "../../../hooks/useSecurityModsec";

const ENGINE_LABEL: Record<ModsecEngineMode, string> = {
  Off: "Off (no inspection)",
  DetectionOnly: "Detection only (logs, allows)",
  On: "On (inspect + block)",
};

export const AdminSecurityModsec = () => {
  const global = useModsecGlobal();
  const updateGlobal = useUpdateModsecGlobal();
  const updateDomain = useUpdateModsecDomain();

  const [page, setPage] = useState(1);
  const pageSize = 20;
  const domains = useModsecDomains(page, pageSize);

  const audit = useModsecAudit(50);

  // Local pending state for the engine + paranoia controls (avoid
  // round-tripping every click; commit via Apply so the operator can
  // drag the slider without 4 nginx reloads).
  const [pendingMode, setPendingMode] = useState<ModsecEngineMode | null>(null);
  const [pendingParanoia, setPendingParanoia] = useState<number | null>(null);

  const effectiveMode: ModsecEngineMode = pendingMode ?? global.data?.engine_mode ?? "Off";
  const effectiveParanoia = pendingParanoia ?? global.data?.paranoia ?? 1;
  const dirty =
    (pendingMode !== null && pendingMode !== global.data?.engine_mode) ||
    (pendingParanoia !== null && pendingParanoia !== global.data?.paranoia);

  const applyGlobal = async () => {
    try {
      await updateGlobal.mutateAsync({
        engine_mode: effectiveMode,
        paranoia: effectiveParanoia,
      });
      message.success("ModSecurity global config updated and nginx reloaded");
      setPendingMode(null);
      setPendingParanoia(null);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to apply");
    }
  };

  const onToggleDomain = async (row: ModsecDomainRow, enabled: boolean) => {
    try {
      await updateDomain.mutateAsync({ id: row.id, modsec_enabled: enabled });
      message.success(`${enabled ? "Enabled" : "Disabled"} ModSec on ${row.name}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to toggle");
    }
  };

  const domainColumns = [
    { title: "Domain", dataIndex: "name", key: "name" },
    {
      title: "ModSec",
      dataIndex: "modsec_enabled",
      key: "modsec_enabled",
      width: 120,
      render: (enabled: boolean, row: ModsecDomainRow) => (
        <Switch
          checked={enabled}
          loading={updateDomain.isPending}
          onChange={(checked) => onToggleDomain(row, checked)}
        />
      ),
    },
  ];

  const auditColumns = [
    {
      title: "When",
      dataIndex: "ts",
      key: "ts",
      width: 200,
      render: (s?: string) => (s ? new Date(s).toLocaleString() : "—"),
    },
    { title: "Client", dataIndex: "client", key: "client", width: 140 },
    { title: "URI", dataIndex: "uri", key: "uri", ellipsis: true },
    {
      title: "Rules",
      dataIndex: "rule_ids",
      key: "rule_ids",
      width: 220,
      render: (ids?: string[]) =>
        ids && ids.length > 0 ? ids.map((id) => <Tag key={id}>{id}</Tag>) : "—",
    },
    {
      title: "Sev",
      dataIndex: "severity",
      key: "severity",
      width: 80,
      render: (s?: string) => s ?? "—",
    },
  ];

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card size="small" title="Global engine">
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <div>
            <Typography.Text strong>Engine mode: </Typography.Text>
            <Space wrap>
              {(["Off", "DetectionOnly", "On"] as ModsecEngineMode[]).map((m) => (
                <Button
                  key={m}
                  type={effectiveMode === m ? "primary" : "default"}
                  size="small"
                  onClick={() => setPendingMode(m)}
                >
                  {ENGINE_LABEL[m]}
                </Button>
              ))}
            </Space>
          </div>
          <div>
            <Typography.Text strong>OWASP CRS paranoia level: </Typography.Text>
            <Typography.Text>{effectiveParanoia} / 4</Typography.Text>
            <Slider
              min={1}
              max={4}
              step={1}
              value={effectiveParanoia}
              onChange={(v) => setPendingParanoia(v)}
              marks={{ 1: "1 (loose)", 2: "2", 3: "3", 4: "4 (strict)" }}
              style={{ maxWidth: 480 }}
            />
          </div>
          <Space>
            <Popconfirm
              title="Apply global ModSec change"
              description="Applies engine_mode + paranoia to /etc/nginx/modsecurity.conf and reloads nginx. Tenant traffic continues with no downtime."
              okText="Apply"
              onConfirm={applyGlobal}
              disabled={!dirty}
            >
              <Button type="primary" disabled={!dirty} loading={updateGlobal.isPending}>
                Apply
              </Button>
            </Popconfirm>
            {dirty && (
              <Button
                onClick={() => {
                  setPendingMode(null);
                  setPendingParanoia(null);
                }}
              >
                Reset
              </Button>
            )}
          </Space>
          {effectiveMode === "On" && (
            <Alert
              type="warning"
              showIcon
              message="Engine mode 'On' will block requests matching CRS rules at the per-domain vhosts that have ModSec enabled. Consider 'DetectionOnly' first to surface false positives."
            />
          )}
        </Space>
      </Card>

      <Card size="small" title="Per-domain enable">
        <Table<ModsecDomainRow>
          rowKey="id"
          dataSource={domains.data?.data ?? []}
          columns={domainColumns}
          loading={domains.isLoading}
          pagination={{
            current: page,
            pageSize,
            total: domains.data?.total ?? 0,
            showSizeChanger: false,
            onChange: (p) => setPage(p),
          }}
          locale={{ emptyText: <Empty description="No domains" /> }}
          scroll={{ x: "max-content" }}
        />
      </Card>

      <Card size="small" title="Audit log (last 50 events)">
        <Table<ModsecAuditEntry>
          rowKey={(_, idx) => String(idx)}
          dataSource={audit.data ?? []}
          columns={auditColumns}
          loading={audit.isLoading}
          pagination={false}
          size="small"
          locale={{
            emptyText: (
              <Empty description="No audit events yet (enable engine + ModSec on a domain to start logging)" />
            ),
          }}
          scroll={{ x: "max-content", y: 360 }}
        />
      </Card>
    </Space>
  );
};
