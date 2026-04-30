// CreateBackupDrawer — admin creates either a per-user account
// backup (kind=account_backup) or a system_backup. The agent
// orchestrator runs the actual stages; the panel just creates the
// workflow row + dispatches.
import { Alert, Button, Drawer, Form, Grid, Input, Radio, Select, Space, message } from "antd";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";
import { useListQuery } from "../../../hooks/useQueries";

type User = {
  id: string;
  username: string;
  email: string;
  is_admin: boolean;
};

type Kind = "account_backup" | "system_backup";

interface CreateBackupDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}

interface FormValues {
  kind: Kind;
  user_id?: string;
  destination_id?: string;
  databases?: string;
  mailboxes?: string;
}

type Destination = { id: string; name: string; kind: string; enabled: boolean };

export const CreateBackupDrawer = ({ open, onClose, onCreated }: CreateBackupDrawerProps) => {
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;
  const [form] = Form.useForm<FormValues>();
  const [submitting, setSubmitting] = useState(false);
  const kind = Form.useWatch("kind", form) ?? "account_backup";

  const usersQuery = useListQuery<User>({
    resource: "admin/users",
    params: { pageSize: 200 },
    enabled: open && kind === "account_backup",
  });
  const destQuery = useListQuery<Destination>({
    resource: "admin/backup-destinations",
    params: { pageSize: 100 },
    enabled: open,
  });

  useEffect(() => {
    if (!open) {
      form.resetFields();
    } else {
      form.setFieldValue("kind", "account_backup");
    }
  }, [open, form]);

  const handleSubmit = async (values: FormValues) => {
    setSubmitting(true);
    try {
      if (!values.destination_id) {
        message.error("Pick a destination");
        return;
      }
      if (values.kind === "system_backup") {
        await apiClient.post(`/admin/system/backups`, {
          include_accounts: false,
          destination_id: values.destination_id,
        });
        message.success("System backup queued");
        onCreated();
        return;
      }
      if (!values.user_id) {
        message.error("Pick a user");
        return;
      }
      const payload = {
        destination_id: values.destination_id,
        databases: values.databases
          ? values.databases.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
        mailboxes: values.mailboxes
          ? values.mailboxes.split(",").map((s) => s.trim()).filter(Boolean)
          : [],
      };
      await apiClient.post(`/admin/users/${values.user_id}/backups`, payload);
      message.success("Backup queued");
      onCreated();
    } catch (err) {
      message.error(extractApiError(err, "Create failed"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Drawer
      title="Create backup"
      open={open}
      onClose={onClose}
      width={isDesktop ? 520 : undefined}
      placement="right"
      destroyOnClose
    >
      <Alert
        type="info"
        showIcon
        message="Backups run as a goroutine inside panel-agent."
        description={
          kind === "system_backup"
            ? "Stages: panel_db (per system DB) → panel_config → service_config → mail_state → tls → security → os_users → data_state → manifest."
            : "Stages: home → databases → mailboxes → metadata → manifest. Each stage produces a separate restic snapshot tagged with the job-id."
        }
        style={{ marginBottom: 16 }}
      />
      <Form<FormValues>
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        initialValues={{ kind: "account_backup" }}
      >
        <Form.Item label="Type" name="kind" rules={[{ required: true }]}>
          <Radio.Group>
            <Radio.Button value="account_backup">Account</Radio.Button>
            <Radio.Button value="system_backup">System</Radio.Button>
          </Radio.Group>
        </Form.Item>

        <Form.Item
          label="Destination"
          name="destination_id"
          rules={[{ required: true, message: "Pick a destination" }]}
          extra="The backup writes directly to this destination — no local source repo."
        >
          <Select
            placeholder="Pick a destination"
            loading={destQuery.isLoading}
            options={(destQuery.items ?? [])
              .filter((d) => d.enabled)
              .map((d) => ({ value: d.id, label: `${d.name} (${d.kind})` }))}
          />
        </Form.Item>

        {kind === "account_backup" && (
          <>
            <Form.Item
              label="User"
              name="user_id"
              rules={[{ required: true, message: "Pick a user" }]}
            >
              <Select
                placeholder="Pick a user"
                showSearch
                optionFilterProp="label"
                loading={usersQuery.isLoading}
                options={(usersQuery.items ?? [])
                  .filter((u) => !u.is_admin)
                  .map((u) => ({
                    value: u.id,
                    label: `${u.username} (${u.email})`,
                  }))}
              />
            </Form.Item>
            <Form.Item
              label="Databases (comma-separated, optional)"
              name="databases"
              extra="Names of databases owned by the user. Leave empty to skip the DB stage."
            >
              <Input placeholder="alice_wp, alice_blog" />
            </Form.Item>
            <Form.Item
              label="Mailboxes (comma-separated, optional)"
              name="mailboxes"
              extra="user@domain pairs. Skips with warning when Stalwart is down."
            >
              <Input placeholder="alice@example.com, hello@example.com" />
            </Form.Item>
          </>
        )}

        <Space>
          <Button type="primary" htmlType="submit" loading={submitting}>
            Create backup
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </Space>
      </Form>
    </Drawer>
  );
};
