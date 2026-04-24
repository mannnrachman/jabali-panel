// AdminSecurityModsec — M26 Step 8. Three cards: global engine + paranoia,
// per-domain toggles (paginated via useTableURL — URL-backed page+
// pageSize, same as every other admin list per CONVENTIONS), audit-log
// tail (auto-refresh 30s).
//
// Tables consume <Table.Column> children, not a columns prop. The
// per-domain table uses SearchableTableStringQ so it gets the
// debounced search bar + scroll={{ x: "max-content" }} for free; the
// audit-log table is a raw <Table> because it has no search box and
// streams a fixed-size tail.
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
  Tabs,
  Tag,
  Typography,
} from "antd";
import { useState } from "react";
import { useSearchParams } from "react-router";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useUpdateMutation } from "../../../hooks/useQueries";
import {
  useModsecAudit,
  useModsecGlobal,
  useUpdateModsecGlobal,
  type ModsecAuditEntry,
  type ModsecDomainRow,
  type ModsecEngineMode,
} from "../../../hooks/useSecurityModsec";
import { useTableURL } from "../../../hooks/useTableURL";

const ENGINE_LABEL: Record<ModsecEngineMode, string> = {
  Off: "Off (no inspection)",
  DetectionOnly: "Detection only (logs, allows)",
  On: "On (inspect + block)",
};

const DOMAINS_RESOURCE = "admin/security/modsec/domains";

const fmtTime = (s?: string): string => (s ? new Date(s).toLocaleString() : "—");

export const AdminSecurityModsec = () => {
  const global = useModsecGlobal();
  const updateGlobal = useUpdateModsecGlobal();

  const domains = useTableURL<ModsecDomainRow>({
    resource: DOMAINS_RESOURCE,
    defaultSort: "name",
    defaultOrder: "asc",
  });
  const updateDomain = useUpdateMutation<ModsecDomainRow, { modsec_enabled: boolean }>({
    resource: DOMAINS_RESOURCE,
  });

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
      await updateDomain.mutateAsync({ id: row.id, input: { modsec_enabled: enabled } });
      message.success(`${enabled ? "Enabled" : "Disabled"} ModSec on ${row.name}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to toggle");
    }
  };

  const [sp, setSp] = useSearchParams();
  const subTabs = ["global", "per-domain", "audit"] as const;
  type SubTab = (typeof subTabs)[number];
  const activeSub: SubTab = (() => {
    const s = sp.get("sub");
    return (subTabs as readonly string[]).includes(s ?? "") ? (s as SubTab) : "global";
  })();
  const onSubChange = (key: string) => {
    setSp((prev) => {
      const next = new URLSearchParams(prev);
      next.set("sub", key);
      return next;
    });
  };

  const globalPanel = (
    <>
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
    </>
  );

  const perDomainPanel = (
    <Card size="small" title="Per-domain enable">
        <SearchableTableStringQ<ModsecDomainRow>
          rowKey="id"
          loading={domains.isLoading}
          dataSource={domains.items}
          initialSearch={domains.params.q}
          searchPlaceholder="Search domains"
          onSearchChange={(q) => domains.setParams({ q, page: 1 })}
          pagination={{
            current: domains.params.page,
            pageSize: domains.params.pageSize,
            total: domains.total,
            showSizeChanger: false,
            onChange: (p) => domains.setParams({ page: p }),
          }}
          locale={{ emptyText: <Empty description="No domains" /> }}
        >
          <Table.Column<ModsecDomainRow> dataIndex="name" title="Domain" key="name" />
          <Table.Column<ModsecDomainRow>
            dataIndex="modsec_enabled"
            title="ModSec"
            key="modsec_enabled"
            width={120}
            render={(enabled: boolean, row) => (
              <Switch
                checked={enabled}
                loading={updateDomain.isPending}
                onChange={(checked) => onToggleDomain(row, checked)}
              />
            )}
          />
        </SearchableTableStringQ>
      </Card>
  );

  const auditPanel = (
    <Card size="small" title="Audit log (last 50 events)">
        <Table<ModsecAuditEntry>
          rowKey={(_, idx) => String(idx)}
          dataSource={audit.data ?? []}
          loading={audit.isLoading}
          pagination={false}
          size="small"
          locale={{
            emptyText: (
              <Empty description="No audit events yet (enable engine + ModSec on a domain to start logging)" />
            ),
          }}
          scroll={{ x: "max-content", y: 360 }}
        >
          <Table.Column<ModsecAuditEntry>
            dataIndex="ts"
            title="When"
            key="ts"
            width={200}
            render={(s?: string) => fmtTime(s)}
          />
          <Table.Column<ModsecAuditEntry> dataIndex="client" title="Client" key="client" width={140} />
          <Table.Column<ModsecAuditEntry> dataIndex="uri" title="URI" key="uri" ellipsis />
          <Table.Column<ModsecAuditEntry>
            dataIndex="rule_ids"
            title="Rules"
            key="rule_ids"
            width={220}
            render={(ids?: string[]) =>
              ids && ids.length > 0 ? ids.map((id) => <Tag key={id}>{id}</Tag>) : "—"
            }
          />
          <Table.Column<ModsecAuditEntry>
            dataIndex="severity"
            title="Sev"
            key="severity"
            width={80}
            render={(s?: string) => s ?? "—"}
          />
        </Table>
      </Card>
  );

  return (
    <Tabs
      activeKey={activeSub}
      onChange={onSubChange}
      items={[
        { key: "global", label: "Global engine", children: globalPanel },
        { key: "per-domain", label: "Per-domain", children: perDomainPanel },
        { key: "audit", label: "Audit log", children: auditPanel },
      ]}
    />
  );
};
