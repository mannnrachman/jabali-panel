// M46 — Database server admin ops UI (Server Settings ▸ Databases tab).
// Rendered as a sibling of DatabasesCard so the opt-in PostgreSQL
// lifecycle card stays untouched. Each M46 step adds a <Card> section
// here. Step 1: root / superuser password (ADR-0097).
//
// Icons go through the @icons shim (CONVENTIONS) — never
// @ant-design/icons.
import { KeyOutlined } from "@icons";
import {
  Button,
  Card,
  Modal,
  Popconfirm,
  Space,
  Typography,
  message,
} from "antd";
import { useState } from "react";

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

export function DatabaseAdminSections() {
  return (
    <>
      <RootPasswordSection />
      {/* M46 Steps 3–6 append config / maintenance / processes /
          admin-SSO sections here. */}
    </>
  );
}
