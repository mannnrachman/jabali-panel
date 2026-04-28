// CreateBackupDrawer — admin picks a user + (optional) databases /
// mailboxes, fires POST /admin/users/:id/backups. The agent
// orchestrator runs the actual stages; the panel just creates the
// workflow row + dispatches.
import { Alert, Button, Drawer, Form, Grid, Input, Select, Space, message } from "antd";
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

interface CreateBackupDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}

interface FormValues {
  user_id: string;
  databases: string;
  mailboxes: string;
}

export const CreateBackupDrawer = ({ open, onClose, onCreated }: CreateBackupDrawerProps) => {
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;
  const [form] = Form.useForm<FormValues>();
  const [submitting, setSubmitting] = useState(false);

  // Naive user picker — full search lands when /admin/users grows past
  // 50 rows in the same install. v1 reuses the standard list endpoint.
  const usersQuery = useListQuery<User>({
    resource: "admin/users",
    params: { pageSize: 200 },
    enabled: open,
  });

  useEffect(() => {
    if (!open) {
      form.resetFields();
    }
  }, [open, form]);

  const handleSubmit = async (values: FormValues) => {
    setSubmitting(true);
    try {
      const payload = {
        databases: values.databases ? values.databases.split(",").map((s) => s.trim()).filter(Boolean) : [],
        mailboxes: values.mailboxes ? values.mailboxes.split(",").map((s) => s.trim()).filter(Boolean) : [],
      };
      await apiClient.post(`/api/v1/admin/users/${values.user_id}/backups`, payload);
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
        message="Backups run as a transient systemd unit and survive jabali update."
        description="Stages: home → databases → mailboxes → manifest. Each stage produces a separate restic snapshot tagged with the job-id."
        style={{ marginBottom: 16 }}
      />
      <Form<FormValues> form={form} layout="vertical" onFinish={handleSubmit}>
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
            options={(usersQuery.items ?? []).map((u) => ({
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
