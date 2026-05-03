// AdminSecuritySnuffleupagus — Security tab card for M41 Snuffleupagus.
// Wave D — full surface: mode toggle, php-version load matrix, recent
// incidents table, per-rule kill switch.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Modal,
  Popconfirm,
  Radio,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";

import {
  type SnuffleupagusIncident,
  type SnuffleupagusMode,
  type SnuffleupagusRule,
  useSetSnuffleupagusMode,
  useSnuffleupagusIncidents,
  useSnuffleupagusRules,
  useSnuffleupagusStatus,
  useToggleSnuffleupagusRule,
} from "../../../hooks/useSecuritySnuffleupagus";

const { Text, Paragraph } = Typography;

const MODE_COLOR: Record<SnuffleupagusMode, string> = {
  off: "default",
  simulation: "warning",
  enforce: "success",
};

const ACTION_COLOR: Record<string, string> = {
  block: "error",
  simulated_block: "warning",
  log: "default",
};

export function AdminSecuritySnuffleupagus() {
  const qc = useQueryClient();
  const status = useSnuffleupagusStatus();
  const incidents = useSnuffleupagusIncidents({ limit: 50 });
  const rules = useSnuffleupagusRules();
  const setMode = useSetSnuffleupagusMode();
  const toggleRule = useToggleSnuffleupagusRule();

  const [rulesOpen, setRulesOpen] = useState(false);

  if (status.isLoading) {
    return (
      <Card title="PHP Defense" size="small">
        <Text type="secondary">Loading…</Text>
      </Card>
    );
  }

  const data = status.data;
  const loadedCount = data?.php_versions_loaded.filter((v) => v.loaded).length ?? 0;
  const totalCount = data?.php_versions_loaded.length ?? 0;

  return (
    <Card
      title="PHP Defense"
      size="small"
      extra={
        <Space>
          {data && <Tag color={MODE_COLOR[data.mode]}>{data.mode}</Tag>}
          <a
            onClick={() => {
              void status.refetch();
              void incidents.refetch();
            }}
          >
            Refresh
          </a>
        </Space>
      }
    >
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <Paragraph type="secondary" style={{ marginBottom: 0 }}>
          PHP Defense (Snuffleupagus) across {loadedCount}/{totalCount} installed PHP minor
          {totalCount === 1 ? "" : "s"}. Rules are maintained by Jabali; mode applies server-wide.
          {data?.last_applied_at && (
            <>
              <br />
              <Text type="secondary">
                Last applied: {new Date(data.last_applied_at).toLocaleString()}
              </Text>
            </>
          )}
        </Paragraph>

        <Space wrap>
          {data?.php_versions_loaded.map((v) => (
            <Tooltip key={v.minor} title={v.extension_so}>
              <Tag color={v.loaded ? "success" : "default"}>
                PHP {v.minor} {v.loaded ? "✓" : "—"}
              </Tag>
            </Tooltip>
          ))}
        </Space>

        <div>
          <Text strong style={{ marginRight: 12 }}>
            Mode:
          </Text>
          <Radio.Group
            value={data?.mode ?? "off"}
            onChange={(e) => {
              const next = e.target.value as SnuffleupagusMode;
              if (next === "enforce") {
                Modal.confirm({
                  title: "Switch PHP Defense to enforce mode?",
                  content:
                    "Tenant requests that match a hardening rule will be blocked. Run a soak in simulation first if you haven't already.",
                  okText: "Enforce",
                  okType: "danger",
                  onOk: () =>
                    setMode.mutateAsync(next).then(() => {
                      void message.success("Mode set to enforce");
                      void qc.invalidateQueries({ queryKey: ["security", "snuffleupagus"] });
                    }),
                });
                return;
              }
              setMode.mutateAsync(next).then(() => {
                void message.success(`Mode set to ${next}`);
                void qc.invalidateQueries({ queryKey: ["security", "snuffleupagus"] });
              });
            }}
            disabled={setMode.isPending}
          >
            <Radio.Button value="off">Off</Radio.Button>
            <Radio.Button value="simulation">Simulation</Radio.Button>
            <Radio.Button value="enforce">Enforce</Radio.Button>
          </Radio.Group>
          <Button type="link" onClick={() => setRulesOpen(true)} style={{ marginLeft: 12 }}>
            Manage rules ({rules.data?.length ?? 0})
          </Button>
        </div>

        {data?.mode === "simulation" && (
          <Alert
            type="info"
            showIcon
            message="Simulation mode active"
            description="Rules log incidents below without blocking. Soak for 7 days to identify false positives, then flip to enforce."
          />
        )}

        <div>
          <Text strong>Recent incidents</Text>
          <Table<SnuffleupagusIncident>
            size="small"
            rowKey="id"
            loading={incidents.isLoading}
            dataSource={incidents.data?.data ?? []}
            pagination={{
              total: incidents.data?.total ?? 0,
              pageSize: 50,
            }}
            columns={[
              {
                title: "When",
                dataIndex: "ts",
                width: 160,
                render: (ts: string) => new Date(ts).toLocaleString(),
              },
              {
                title: "Action",
                dataIndex: "action",
                width: 130,
                render: (a: string) => <Tag color={ACTION_COLOR[a] ?? "default"}>{a}</Tag>,
              },
              { title: "Rule", dataIndex: "rule_name", ellipsis: true },
              { title: "PHP", dataIndex: "php_version", width: 70 },
              { title: "Source", dataIndex: "source_ip", width: 140 },
              { title: "URI", dataIndex: "request_uri", ellipsis: true },
            ]}
            scroll={{ x: "max-content" }}
            locale={{ emptyText: "No incidents yet" }}
          />
        </div>
      </Space>

      <Modal
        title="PHP Defense rules"
        open={rulesOpen}
        onCancel={() => setRulesOpen(false)}
        footer={<Button onClick={() => setRulesOpen(false)}>Close</Button>}
        width={900}
      >
        <Paragraph type="secondary">
          Toggle a rule off when triaging a false positive. Disabled rules are excluded from the
          active.rules render on the next reconcile.
        </Paragraph>
        <Table<SnuffleupagusRule>
          size="small"
          rowKey="name"
          loading={rules.isLoading}
          dataSource={rules.data ?? []}
          pagination={{ pageSize: 25 }}
          columns={[
            { title: "Rule", dataIndex: "name", ellipsis: true },
            { title: "Source", dataIndex: "source_file", width: 180 },
            {
              title: "Enabled",
              dataIndex: "enabled",
              width: 110,
              render: (_: boolean, row: SnuffleupagusRule) => (
                <Popconfirm
                  title={row.enabled ? "Disable this rule?" : "Re-enable this rule?"}
                  onConfirm={() =>
                    toggleRule
                      .mutateAsync({ name: row.name, enabled: !row.enabled })
                      .then(() => {
                        void message.success(`Rule ${row.enabled ? "disabled" : "enabled"}`);
                        void qc.invalidateQueries({ queryKey: ["security", "snuffleupagus"] });
                      })
                  }
                >
                  <Switch checked={row.enabled} loading={toggleRule.isPending} />
                </Popconfirm>
              ),
            },
            { title: "Reason", dataIndex: "reason", ellipsis: true },
          ]}
          scroll={{ x: "max-content" }}
        />
      </Modal>
    </Card>
  );
}
