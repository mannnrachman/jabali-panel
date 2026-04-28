// M30.1 Destinations admin tab. Lists every backup_destinations row,
// drawer for create/edit, "Test" button verifies creds against the
// remote via `restic snapshots`.
import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import {
  CheckCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from "@icons";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";

interface BackupDestination {
  id: string;
  name: string;
  kind: "local" | "sftp" | "s3" | "b2" | "azure" | "gcs" | "rest";
  url: string;
  has_credentials: boolean;
  credentials_keys_mask?: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

const KIND_OPTIONS: Array<{ value: BackupDestination["kind"]; label: string }> = [
  { value: "local", label: "Local (this server)" },
  { value: "sftp", label: "SFTP" },
  { value: "s3", label: "S3-compatible (AWS, MinIO, Wasabi, R2)" },
  { value: "b2", label: "Backblaze B2 (native)" },
  { value: "azure", label: "Azure Blob Storage" },
  { value: "gcs", label: "Google Cloud Storage" },
  { value: "rest", label: "Restic REST server" },
];

const URL_HINT: Record<BackupDestination["kind"], string> = {
  local: "/var/lib/jabali-backups/repo",
  sftp: "sftp:user@host:/path/to/repo",
  s3: "s3:s3.amazonaws.com/bucket/path",
  b2: "b2:bucket-name:path",
  azure: "azure:container:/path",
  gcs: "gs:bucket-name:/path",
  rest: "rest:https://restic-server.example.com/repo/",
};

const ENV_HINT: Record<BackupDestination["kind"], string> = {
  local: "(none)",
  sftp: "(none — uses SSH keys from /root/.ssh)",
  s3: "AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY",
  b2: "B2_ACCOUNT_ID, B2_ACCOUNT_KEY",
  azure: "AZURE_ACCOUNT_NAME, AZURE_ACCOUNT_KEY",
  gcs: "GOOGLE_APPLICATION_CREDENTIALS (path)",
  rest: "(basic auth via URL or cert)",
};

interface DestinationDrawerProps {
  open: boolean;
  editing: BackupDestination | null;
  onClose: () => void;
  onSaved: () => void;
}

function DestinationDrawer({ open, editing, onClose, onSaved }: DestinationDrawerProps) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      form.resetFields();
      if (editing) {
        form.setFieldsValue({
          name: editing.name,
          kind: editing.kind,
          url: editing.url,
          enabled: editing.enabled,
        });
      } else {
        form.setFieldsValue({ kind: "s3", enabled: true });
      }
    }
  }, [open, editing, form]);

  const handleSave = async () => {
    let values;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setBusy(true);
    try {
      const credentialsEnv = parseEnvBlock(values.credentials_env_block || "");
      const body: Record<string, unknown> = {
        name: values.name,
        kind: values.kind,
        url: values.url,
        enabled: values.enabled,
      };
      if (Object.keys(credentialsEnv).length > 0) {
        body.credentials_env = credentialsEnv;
      }
      if (editing) {
        await apiClient.patch(`/api/v1/admin/backup-destinations/${editing.id}`, body);
        message.success("Destination updated");
      } else {
        await apiClient.post("/api/v1/admin/backup-destinations", body);
        message.success("Destination created");
      }
      onSaved();
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  const kindWatch: BackupDestination["kind"] = Form.useWatch("kind", form) || "s3";

  return (
    <Drawer
      title={editing ? `Edit destination — ${editing.name}` : "New destination"}
      width={520}
      open={open}
      onClose={onClose}
      destroyOnClose
      extra={
        <Space>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="primary" loading={busy} onClick={handleSave}>
            Save
          </Button>
        </Space>
      }
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label="Name" rules={[{ required: true }]}>
          <Input placeholder="offsite-s3" />
        </Form.Item>
        <Form.Item name="kind" label="Backend" rules={[{ required: true }]}>
          <Select options={KIND_OPTIONS} />
        </Form.Item>
        <Form.Item
          name="url"
          label="Restic URL"
          rules={[{ required: true }]}
          extra={`Format: ${URL_HINT[kindWatch]}`}
        >
          <Input placeholder={URL_HINT[kindWatch]} />
        </Form.Item>
        <Form.Item
          name="credentials_env_block"
          label="Credentials (KEY=VALUE per line)"
          extra={`Required env vars: ${ENV_HINT[kindWatch]}`}
        >
          <Input.TextArea
            rows={5}
            placeholder={
              kindWatch === "s3"
                ? "AWS_ACCESS_KEY_ID=AKIA…\nAWS_SECRET_ACCESS_KEY=…"
                : ENV_HINT[kindWatch]
            }
          />
        </Form.Item>
        {editing?.has_credentials && (
          <Typography.Text type="secondary">
            Stored credentials: {editing.credentials_keys_mask?.join(", ") || "(redacted)"}.
            Leave the field blank to keep them; fill it to overwrite.
          </Typography.Text>
        )}
        <Form.Item name="enabled" label="Enabled" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Drawer>
  );
}

function parseEnvBlock(block: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of block.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    const value = line.slice(eq + 1).trim();
    if (key && value) out[key] = value;
  }
  return out;
}

export function DestinationsTab() {
  const [rows, setRows] = useState<BackupDestination[]>([]);
  const [loading, setLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<BackupDestination | null>(null);

  const reload = async () => {
    setLoading(true);
    try {
      const resp = await apiClient.get<{ data: BackupDestination[] }>(
        "/api/v1/admin/backup-destinations",
      );
      setRows(resp.data.data ?? []);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Load failed");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void reload();
  }, []);

  const handleDelete = async (row: BackupDestination) => {
    Modal.confirm({
      title: `Delete destination "${row.name}"?`,
      content:
        "Existing backups copied to this destination remain on the remote (panel does not " +
        "auto-purge remote snapshots). Schedules linked to this destination will simply " +
        "stop copying to it.",
      okType: "danger",
      onOk: async () => {
        try {
          await apiClient.delete(`/api/v1/admin/backup-destinations/${row.id}`);
          message.success(`Deleted ${row.name}`);
          void reload();
        } catch (err) {
          message.error(err instanceof Error ? err.message : "Delete failed");
        }
      },
    });
  };

  const handleTest = async (row: BackupDestination) => {
    const hide = message.loading(`Testing ${row.name}…`, 0);
    try {
      const resp = await apiClient.post<{ status: string; detail?: string }>(
        `/api/v1/admin/backup-destinations/${row.id}/test`,
        {},
      );
      hide();
      const detail = resp.data.detail;
      message.success(detail ? `OK — ${detail}` : "Connection OK");
    } catch (err) {
      hide();
      message.error(err instanceof Error ? err.message : "Test failed");
    }
  };

  return (
    <Card>
      <Space style={{ marginBottom: 12 }}>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            setEditing(null);
            setDrawerOpen(true);
          }}
        >
          New destination
        </Button>
      </Space>
      <Table<BackupDestination>
        rowKey="id"
        loading={loading}
        dataSource={rows}
        pagination={false}
        scroll={{ x: "max-content" }}
      >
        <Table.Column dataIndex="name" title="Name" />
        <Table.Column
          dataIndex="kind"
          title="Backend"
          render={(k: string) => <Tag>{k.toUpperCase()}</Tag>}
        />
        <Table.Column dataIndex="url" title="URL" />
        <Table.Column
          dataIndex="enabled"
          title="Enabled"
          render={(v: boolean) => (v ? <Tag color="green">yes</Tag> : <Tag>no</Tag>)}
        />
        <Table.Column
          dataIndex="has_credentials"
          title="Credentials"
          render={(v: boolean) =>
            v ? <CheckCircleOutlined style={{ color: "#52c41a" }} /> : "—"
          }
        />
        <Table.Column<BackupDestination>
          title="Actions"
          render={(_, row) => (
            <Space>
              <Button
                size="small"
                icon={<ThunderboltOutlined />}
                onClick={() => handleTest(row)}
              >
                Test
              </Button>
              <Button
                size="small"
                icon={<EditOutlined />}
                onClick={() => {
                  setEditing(row);
                  setDrawerOpen(true);
                }}
              >
                Edit
              </Button>
              <Button
                size="small"
                danger
                icon={<DeleteOutlined />}
                onClick={() => handleDelete(row)}
              />
            </Space>
          )}
        />
      </Table>
      <DestinationDrawer
        open={drawerOpen}
        editing={editing}
        onClose={() => setDrawerOpen(false)}
        onSaved={() => {
          setDrawerOpen(false);
          void reload();
        }}
      />
    </Card>
  );
}
