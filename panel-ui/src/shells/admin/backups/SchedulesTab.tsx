// M30.1 Schedules admin tab. Lists every backup_schedules row, drawer
// for create/edit, multi-select destinations, "Run now" button.
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import {
  CalendarCheckOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  PlayCircleOutlined,
} from "@icons";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";

interface BackupSchedule {
  id: string;
  kind: "account_backup" | "system_backup";
  user_id?: string | null;
  include_system_backup?: boolean;
  cron_expr: string;
  enabled: boolean;
  keep_daily?: number | null;
  keep_weekly?: number | null;
  keep_monthly?: number | null;
  last_run_at?: string | null;
  next_run_at?: string | null;
  next_firings?: string[];
  destinations?: Array<{ id: string; name: string; kind: string; enabled: boolean }>;
}

interface BackupDestinationOption {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
}

interface User {
  id: string;
  username: string;
  email: string;
  is_admin?: boolean;
}

// Sentinel value used for the "All non-admin users" picker option.
// Wire-shape on POST/PATCH is `user_id: null`; the form sends "" and
// the submit handler omits user_id from the body.
const ALL_USERS = "__all__";

const PRESETS: Array<{ label: string; value: string; cron: string }> = [
  { label: "Daily 03:00", value: "daily", cron: "0 3 * * *" },
  { label: "Weekly Sun 03:00", value: "weekly", cron: "0 3 * * 0" },
  { label: "Monthly 1st 03:00", value: "monthly", cron: "0 3 1 * *" },
  { label: "Custom", value: "custom", cron: "" },
];

interface ScheduleDrawerProps {
  open: boolean;
  editing: BackupSchedule | null;
  destinations: BackupDestinationOption[];
  users: User[];
  onClose: () => void;
  onSaved: () => void;
}

function ScheduleDrawer({
  open,
  editing,
  destinations,
  users,
  onClose,
  onSaved,
}: ScheduleDrawerProps) {
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [preset, setPreset] = useState<string>("daily");

  useEffect(() => {
    if (open) {
      form.resetFields();
      if (editing) {
        const matched = PRESETS.find((p) => p.cron === editing.cron_expr);
        setPreset(matched?.value ?? "custom");
        form.setFieldsValue({
          kind: editing.kind,
          user_id: editing.kind === "account_backup"
            ? (editing.user_id ?? ALL_USERS)
            : undefined,
          cron_expr: editing.cron_expr,
          enabled: editing.enabled,
          keep_daily: editing.keep_daily ?? undefined,
          keep_weekly: editing.keep_weekly ?? undefined,
          keep_monthly: editing.keep_monthly ?? undefined,
          include_system_backup: editing.include_system_backup ?? false,
          destination_ids: editing.destinations?.map((d) => d.id) ?? [],
        });
      } else {
        setPreset("daily");
        form.setFieldsValue({
          kind: "account_backup",
          cron_expr: "0 3 * * *",
          enabled: true,
          destination_ids: [],
        });
      }
    }
  }, [open, editing, form]);

  const handlePresetChange = (value: string) => {
    setPreset(value);
    const p = PRESETS.find((x) => x.value === value);
    if (p && p.cron) {
      form.setFieldsValue({ cron_expr: p.cron });
    }
  };

  const handleSave = async () => {
    let values;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        kind: values.kind,
        cron_expr: values.cron_expr,
        enabled: values.enabled,
        destination_ids: values.destination_ids ?? [],
      };
      if (values.kind === "account_backup") {
        if (!values.user_id) {
          message.error("Pick a user (or 'All users') for account schedules");
          setBusy(false);
          return;
        }
        // ALL_USERS sentinel = omit user_id so the backend interprets
        // it as fan-out across every non-admin user at tick time.
        if (values.user_id !== ALL_USERS) {
          body.user_id = values.user_id;
        }
        body.include_system_backup = !!values.include_system_backup;
      }
      if (values.keep_daily !== undefined) body.keep_daily = values.keep_daily;
      if (values.keep_weekly !== undefined) body.keep_weekly = values.keep_weekly;
      if (values.keep_monthly !== undefined) body.keep_monthly = values.keep_monthly;

      if (editing) {
        await apiClient.patch(`/admin/backup-schedules/${editing.id}`, body);
        message.success("Schedule updated");
      } else {
        await apiClient.post("/admin/backup-schedules", body);
        message.success("Schedule created");
      }
      onSaved();
    } catch (err) {
      message.error(extractApiError(err, "Save failed"));
    } finally {
      setBusy(false);
    }
  };

  const kindWatch = Form.useWatch("kind", form);

  return (
    <Drawer
      title={editing ? "Edit schedule" : "New schedule"}
      width={560}
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
        <Form.Item name="kind" label="Backup kind" rules={[{ required: true }]}>
          <Radio.Group>
            <Radio.Button value="account_backup">Account</Radio.Button>
            <Radio.Button value="system_backup">System</Radio.Button>
          </Radio.Group>
        </Form.Item>
        {kindWatch === "account_backup" && (
          <>
            <Form.Item
              name="user_id"
              label="User"
              rules={[{ required: true, message: "User is required for account schedules" }]}
            >
              <Select
                showSearch
                placeholder="Select user"
                optionFilterProp="label"
                options={[
                  { value: ALL_USERS, label: "All users (every non-admin)" },
                  ...users
                    .filter((u) => !u.is_admin)
                    .map((u) => ({
                      value: u.id,
                      label: `${u.username} (${u.email})`,
                    })),
                ]}
              />
            </Form.Item>
            <Form.Item
              name="include_system_backup"
              label="Include system backup"
              valuePropName="checked"
              extra="Also fire a system backup (panel DBs + service config + mail state + …) every time this schedule runs."
            >
              <Switch />
            </Form.Item>
          </>
        )}
        <Form.Item label="Cadence">
          <Radio.Group
            value={preset}
            onChange={(e) => handlePresetChange(e.target.value)}
            buttonStyle="solid"
          >
            {PRESETS.map((p) => (
              <Radio.Button key={p.value} value={p.value}>
                {p.label}
              </Radio.Button>
            ))}
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="cron_expr"
          label="Cron expression"
          rules={[{ required: true }]}
          extra="5-field cron (minute hour day month dow). Server validates before save."
        >
          <Input placeholder="0 3 * * *" disabled={preset !== "custom"} />
        </Form.Item>
        <Form.Item
          name="destination_ids"
          label="Destinations"
          extra={
            destinations.length === 0
              ? "Create at least one destination first."
              : "Local repo always receives a copy. Pick remotes to mirror to."
          }
        >
          <Select
            mode="multiple"
            placeholder="Select destinations"
            options={destinations.map((d) => ({
              value: d.id,
              label: `${d.name} (${d.kind})${d.enabled ? "" : " — disabled"}`,
              disabled: !d.enabled,
            }))}
          />
        </Form.Item>

        <Typography.Title level={5}>Retention overrides (advanced)</Typography.Title>
        <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
          Leave blank to inherit server defaults from server_settings.
          backup_keep_*.
        </Typography.Paragraph>
        <Space size={12} style={{ display: "flex", flexWrap: "wrap" }}>
          <Form.Item name="keep_daily" label="Keep daily">
            <InputNumber min={0} max={365} placeholder="inherit" />
          </Form.Item>
          <Form.Item name="keep_weekly" label="Keep weekly">
            <InputNumber min={0} max={52} placeholder="inherit" />
          </Form.Item>
          <Form.Item name="keep_monthly" label="Keep monthly">
            <InputNumber min={0} max={120} placeholder="inherit" />
          </Form.Item>
        </Space>

        <Form.Item name="enabled" label="Enabled" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Drawer>
  );
}

export function SchedulesTab() {
  const [rows, setRows] = useState<BackupSchedule[]>([]);
  const [destinations, setDestinations] = useState<BackupDestinationOption[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<BackupSchedule | null>(null);

  const reload = async () => {
    setLoading(true);
    try {
      const [s, d, u] = await Promise.all([
        apiClient.get<{ data: BackupSchedule[] }>("/admin/backup-schedules"),
        apiClient.get<{ data: BackupDestinationOption[] }>(
          "/admin/backup-destinations",
        ),
        apiClient.get<{ data: User[] }>("/users?page_size=500"),
      ]);
      setRows(s.data.data ?? []);
      setDestinations(d.data.data ?? []);
      setUsers(u.data.data ?? []);
    } catch (err) {
      message.error(extractApiError(err, "Load failed"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void reload();
  }, []);

  const handleDelete = async (row: BackupSchedule) => {
    Modal.confirm({
      title: `Delete schedule?`,
      content: `Backups already produced by this schedule remain. Future runs will not fire.`,
      okType: "danger",
      onOk: async () => {
        try {
          await apiClient.delete(`/admin/backup-schedules/${row.id}`);
          message.success("Schedule deleted");
          void reload();
        } catch (err) {
          message.error(extractApiError(err, "Delete failed"));
        }
      },
    });
  };

  const handleRunNow = async (row: BackupSchedule) => {
    try {
      await apiClient.post(`/admin/backup-schedules/${row.id}/run-now`, {});
      message.success("Schedule queued for the next tick (within 60s)");
      void reload();
    } catch (err) {
      message.error(extractApiError(err, "Run-now failed"));
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
          disabled={destinations.length === 0}
        >
          New schedule
        </Button>
      </Space>
      {destinations.length === 0 && (
        <Alert
          showIcon
          type="warning"
          style={{ marginBottom: 12 }}
          message="Create at least one destination before adding schedules."
        />
      )}
      <Table<BackupSchedule>
        rowKey="id"
        loading={loading}
        dataSource={rows}
        pagination={false}
        scroll={{ x: "max-content" }}
      >
        <Table.Column dataIndex="kind" title="Kind" render={(k: string) => <Tag>{k}</Tag>} />
        <Table.Column<BackupSchedule>
          title="User"
          render={(_, row) => {
            if (row.kind === "system_backup") return "—";
            if (!row.user_id) return <Tag color="blue">all users</Tag>;
            return (
              users.find((u) => u.id === row.user_id)?.username ??
              <code>{row.user_id.slice(0, 8)}…</code>
            );
          }}
        />
        <Table.Column
          dataIndex="cron_expr"
          title="Cron"
          render={(c: string, row: BackupSchedule) => (
            <Tooltip
              title={
                row.next_firings && row.next_firings.length > 0
                  ? `Next firings:\n${row.next_firings.join("\n")}`
                  : ""
              }
            >
              <code>{c}</code>
            </Tooltip>
          )}
        />
        <Table.Column
          dataIndex="enabled"
          title="Enabled"
          render={(v: boolean) => (v ? <Tag color="green">yes</Tag> : <Tag>no</Tag>)}
        />
        <Table.Column<BackupSchedule>
          title="Destinations"
          render={(_, row) => (
            <Space wrap>
              {(row.destinations ?? []).map((d) => (
                <Tag key={d.id}>
                  {d.name} ({d.kind})
                </Tag>
              ))}
              {(row.destinations ?? []).length === 0 && (
                <Typography.Text type="secondary">local-only</Typography.Text>
              )}
            </Space>
          )}
        />
        <Table.Column
          dataIndex="next_run_at"
          title="Next run"
          render={(v: string | null) => v ?? "—"}
        />
        <Table.Column
          dataIndex="last_run_at"
          title="Last run"
          render={(v: string | null) => v ?? "—"}
        />
        <Table.Column<BackupSchedule>
          title="Actions"
          render={(_, row) => (
            <Space>
              <Button
                size="small"
                icon={<PlayCircleOutlined />}
                onClick={() => handleRunNow(row)}
                disabled={!row.enabled}
              >
                Run now
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
              <Button size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(row)} />
            </Space>
          )}
        />
      </Table>
      <ScheduleDrawer
        open={drawerOpen}
        editing={editing}
        destinations={destinations}
        users={users}
        onClose={() => setDrawerOpen(false)}
        onSaved={() => {
          setDrawerOpen(false);
          void reload();
        }}
      />
      <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>
        <CalendarCheckOutlined /> Tick cadence: 60s. Local backup runs first; remote{" "}
        <code>restic copy</code> jobs are queued asynchronously after the local
        backup seals its manifest snapshot.
      </Typography.Paragraph>
    </Card>
  );
}
