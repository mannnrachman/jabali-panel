// DatabaseUserDrawer — create-user Drawer (replaces the per-shell
// /database-users/create page route). On success the reveal-once
// password modal is shown — the form drawer closes first so the
// modal isn't trapped inside it.
import { useEffect, useState } from "react";
import { Button, Drawer, Form, Grid, Input, Segmented, Space, message } from "antd";

import { apiClient } from "../../../apiClient";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";

type CreateInput = { username: string; engine?: "mariadb" | "postgres" };
type CreateResponse = { id: string; username: string; password: string };

export interface DatabaseUserDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated?: () => void;
}

export const DatabaseUserDrawer = ({
  open,
  onClose,
  onCreated,
}: DatabaseUserDrawerProps) => {
  const [form] = Form.useForm<CreateInput>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;
  const [submitting, setSubmitting] = useState(false);
  const [revealed, setRevealed] = useState<{ username: string; password: string } | null>(null);
  // M37 Phase 4: hide engine picker if PostgreSQL is not server-enabled.
  const [postgresEnabled, setPostgresEnabled] = useState(false);

  useEffect(() => {
    if (!open) return;
    form.resetFields();
    apiClient
      .get<{ postgres_enabled: boolean }>("/me/server-capabilities")
      .then((r) => setPostgresEnabled(!!r.data.postgres_enabled))
      .catch(() => setPostgresEnabled(false));
  }, [open, form]);

  const onFinish = async (values: CreateInput) => {
    setSubmitting(true);
    try {
      const resp = await apiClient.post<CreateResponse>("/database-users", values);
      setRevealed({ username: resp.data.username, password: resp.data.password });
      onClose();
      onCreated?.();
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error ??
        "Failed to create database user";
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
      <Drawer
        title="Create database user"
        open={open}
        onClose={onClose}
        width={isDesktop ? 480 : undefined}
        placement="right"
        destroyOnClose
      >
        <Form<CreateInput> form={form} layout="vertical" onFinish={onFinish} initialValues={{ engine: "mariadb" }}>
          {postgresEnabled && (
            <Form.Item label="Engine" name="engine" tooltip="MariaDB is the default. PostgreSQL must be enabled in Server Settings.">
              <Segmented options={[{ label: "MariaDB", value: "mariadb" }, { label: "PostgreSQL", value: "postgres" }]} />
            </Form.Item>
          )}
          <Form.Item
            label="Username"
            name="username"
            rules={[
              { required: true, message: "Username is required" },
              {
                pattern: /^[a-z][a-z0-9_]{0,30}$/,
                message:
                  "Lowercase letters, digits and underscores only; must start with a letter; max 30 chars",
              },
            ]}
            tooltip="The final MariaDB username will be your panel username plus an underscore plus this value (e.g. alice_api)."
            extra="Your username will be prepended automatically."
          >
            <Input placeholder="e.g. api" autoComplete="off" />
          </Form.Item>

          <Form.Item>
            <Space>
              <Button type="primary" htmlType="submit" loading={submitting}>
                Create
              </Button>
              <Button onClick={onClose}>Cancel</Button>
            </Space>
          </Form.Item>
        </Form>
      </Drawer>

      <DatabaseUserPasswordModal
        open={revealed !== null}
        username={revealed?.username ?? ""}
        password={revealed?.password ?? ""}
        title="Database user created"
        onClose={() => setRevealed(null)}
      />
    </>
  );
};
