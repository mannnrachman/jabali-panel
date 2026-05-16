// M46 — Database server admin ops UI (Server Settings ▸ Databases tab).
// Rendered as a sibling of DatabasesCard so the opt-in PostgreSQL
// lifecycle card stays untouched. Each M46 step adds a <Card> section
// here. Step 1: root / superuser password (ADR-0097).
//
// Icons go through the @icons shim (CONVENTIONS) — never
// @ant-design/icons.
import { DatabaseOutlined, KeyOutlined, SettingOutlined } from "@icons";
import {
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Segmented,
  Select,
  Skeleton,
  Space,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";

type Engine = "mariadb" | "postgres";

interface RootPasswordResponse {
  password: string;
}

const ENGINE_LABEL: Record<Engine, string> = {
  mariadb: "MariaDB (root)",
  postgres: "PostgreSQL (postgres)",
};

function RootPasswordSection() {
  const [busy, setBusy] = useState<Engine | null>(null);
  const [revealed, setRevealed] = useState<{ engine: Engine; password: string } | null>(
    null,
  );

  const rotate = async (engine: Engine) => {
    setBusy(engine);
    try {
      const res = await apiClient.post<RootPasswordResponse>(
        "/admin/databases/root-password",
        { engine },
      );
      setRevealed({ engine, password: res.data.password });
    } catch (err) {
      message.error(
        `Could not rotate ${ENGINE_LABEL[engine]} password: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card
      title={
        <Space>
          <KeyOutlined />
          Root / superuser password
        </Space>
      }
      style={{ marginBottom: 16 }}
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        Sets a break-glass password <strong>alongside</strong> the existing
        socket / peer authentication — the panel keeps connecting over the
        local socket either way, so this never locks the panel out. The
        password is shown once; store it now.
      </Typography.Paragraph>
      <Space wrap>
        {(Object.keys(ENGINE_LABEL) as Engine[]).map((engine) => (
          <Popconfirm
            key={engine}
            title={`Rotate the ${ENGINE_LABEL[engine]} password?`}
            description="The previous password (if any) stops working immediately."
            okText="Rotate"
            okButtonProps={{ danger: true }}
            onConfirm={() => rotate(engine)}
          >
            <Button danger loading={busy === engine}>
              Set / rotate {ENGINE_LABEL[engine]} password
            </Button>
          </Popconfirm>
        ))}
      </Space>

      <Typography.Paragraph
        type="secondary"
        style={{ marginTop: 12, marginBottom: 0, fontSize: 12 }}
      >
        Per-database <em>user</em> passwords (not the root/superuser) are
        rotated from the Databases page — each database user has a
        reveal-once “Password” action there.
      </Typography.Paragraph>

      <Modal
        open={revealed != null}
        title={
          revealed ? `New ${ENGINE_LABEL[revealed.engine]} password` : ""
        }
        onCancel={() => setRevealed(null)}
        onOk={() => setRevealed(null)}
        okText="I saved it"
        cancelButtonProps={{ style: { display: "none" } }}
        maskClosable={false}
      >
        <Typography.Paragraph type="warning">
          This is shown <strong>once</strong>. It is not stored in the panel
          and cannot be retrieved later.
        </Typography.Paragraph>
        <Typography.Paragraph
          copyable={{ text: revealed?.password ?? "" }}
          code
          style={{ fontSize: 15, wordBreak: "break-all" }}
        >
          {revealed?.password}
        </Typography.Paragraph>
      </Modal>
    </Card>
  );
}

interface ConfigParam {
  name: string;
  kind: "int" | "bytes" | "bool" | "float";
  min: number;
  max: number;
  unit: string;
  restart_required: boolean;
  default: string;
  help: string;
  value: string;
}

function ConfigTunerSection() {
  const [engine, setEngine] = useState<Engine>("mariadb");
  const [params, setParams] = useState<ConfigParam[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm();

  const load = async (e: Engine) => {
    setLoading(true);
    setParams(null);
    try {
      const res = await apiClient.get<{ data: ConfigParam[] }>(
        `/admin/databases/config?engine=${e}`,
      );
      setParams(res.data.data);
      form.setFieldsValue(
        Object.fromEntries(res.data.data.map((p) => [p.name, p.value])),
      );
    } catch (err) {
      message.error(
        `Could not load ${e} config: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load(engine);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [engine]);

  const apply = async () => {
    const values = form.getFieldsValue();
    const settings: Record<string, string> = {};
    for (const p of params ?? []) {
      const v = values[p.name];
      if (v !== undefined && v !== null && String(v) !== "") {
        settings[p.name] = String(v);
      }
    }
    setSaving(true);
    try {
      await apiClient.put("/admin/databases/config", { engine, settings });
      message.success(`${engine} configuration applied.`);
      void load(engine);
    } catch (err) {
      message.error(
        `Apply failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    } finally {
      setSaving(false);
    }
  };

  const anyRestart = (params ?? []).some((p) => p.restart_required);

  return (
    <Card
      title={
        <Space>
          <SettingOutlined />
          Database configuration
        </Space>
      }
      style={{ marginBottom: 16 }}
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        Curated, range-checked tuning only — no raw config editing. Changes
        are validated and rolled back automatically if the server fails to
        come back up. Keys marked <Tag color="orange">restart</Tag> bounce
        the service: every site’s DB connections drop for a few seconds.
      </Typography.Paragraph>

      <Segmented
        options={[
          { label: "MariaDB", value: "mariadb" },
          { label: "PostgreSQL", value: "postgres" },
        ]}
        value={engine}
        onChange={(v) => setEngine(v as Engine)}
        style={{ marginBottom: 16 }}
      />

      {loading || params == null ? (
        <Skeleton active paragraph={{ rows: 6 }} />
      ) : (
        <Form form={form} layout="vertical">
          {params.map((p) => (
            <Form.Item
              key={p.name}
              name={p.name}
              label={
                <Space size={4}>
                  <code>{p.name}</code>
                  {p.unit && (
                    <Typography.Text type="secondary">
                      ({p.unit})
                    </Typography.Text>
                  )}
                  {p.restart_required && <Tag color="orange">restart</Tag>}
                </Space>
              }
              help={p.help}
            >
              {p.kind === "bool" ? (
                <Select
                  options={
                    engine === "mariadb"
                      ? [
                          { value: "0", label: "Off (0)" },
                          { value: "1", label: "On (1)" },
                        ]
                      : [
                          { value: "off", label: "off" },
                          { value: "on", label: "on" },
                        ]
                  }
                  style={{ maxWidth: 220 }}
                />
              ) : p.kind === "int" || p.kind === "float" ? (
                <InputNumber
                  min={p.min}
                  max={p.max}
                  step={p.kind === "float" ? 0.1 : 1}
                  style={{ maxWidth: 260 }}
                />
              ) : (
                <Input
                  style={{ maxWidth: 260 }}
                  placeholder={`${p.default} (bytes; K/M/G suffix ok)`}
                />
              )}
            </Form.Item>
          ))}

          <Popconfirm
            title={`Apply ${engine} configuration?`}
            description={
              anyRestart
                ? "Some changed keys require a service restart — DB connections will drop briefly."
                : "Configuration will be reloaded."
            }
            okText="Apply"
            onConfirm={apply}
          >
            <Button type="primary" loading={saving}>
              Apply {engine} configuration
            </Button>
          </Popconfirm>
        </Form>
      )}
    </Card>
  );
}

function AdminDbConsoleSection() {
  const [busy, setBusy] = useState<string | null>(null);

  const open = async (path: string, label: string) => {
    setBusy(label);
    try {
      const res = await apiClient.post<{ redirect_url: string }>(path, {});
      window.open(res.data.redirect_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      message.error(
        `Could not open ${label}: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card
      title={
        <Space>
          <DatabaseOutlined />
          Database console (all databases)
        </Space>
      }
      style={{ marginBottom: 16 }}
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        Opens a privileged session that can see and edit{" "}
        <strong>every database on this server</strong>. This is a
        root-equivalent web shell — single-use, short-lived, admin-only,
        and audited. Treat it accordingly.
      </Typography.Paragraph>
      <Space wrap>
        <Button
          loading={busy === "phpMyAdmin"}
          onClick={() => open("/admin/databases/sso/phpmyadmin", "phpMyAdmin")}
        >
          Open phpMyAdmin (all MariaDB)
        </Button>
        <Button
          loading={busy === "Adminer"}
          onClick={() => open("/admin/databases/sso/adminer", "Adminer")}
        >
          Open Adminer (all PostgreSQL)
        </Button>
      </Space>
    </Card>
  );
}

export function DatabaseAdminSections() {
  return (
    <>
      <RootPasswordSection />
      <ConfigTunerSection />
      <AdminDbConsoleSection />
      {/* M46 Steps 5–6 append maintenance / processes sections here. */}
    </>
  );
}
