// AdminSecurityEgress — M34 admin tab. Lists every user with their
// egress policy state, drop count, and edit Drawer. Pending requests
// panel below the table; approve/deny inline.
//
// Backed by panel-api/internal/api/user_egress.go.
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";

import { useListQuery } from "../../../hooks/useQueries";
import {
  type EgressDestination,
  type EgressRequest,
  type EgressState,
  useDecideEgressRequest,
  useEgressSummary,
  usePendingEgressRequests,
  useUpdateUserEgress,
  useUserEgressPolicy,
} from "../../../hooks/useUserEgress";

type UserRow = {
  id: string;
  username?: string | null;
  email: string;
  is_admin: boolean;
};

const STATE_TAG: Record<EgressState, { color: string; label: string }> = {
  off: { color: "default", label: "OFF" },
  learning: { color: "gold", label: "LEARNING" },
  enforced: { color: "green", label: "ENFORCED" },
};

const STATE_OPTIONS: { value: EgressState; label: string }[] = [
  { value: "off", label: "Off (no filter)" },
  { value: "learning", label: "Learning (log only)" },
  { value: "enforced", label: "Enforced (drop)" },
];

const PROTOCOL_OPTIONS = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
];

const renderStateTag = (state: EgressState) => {
  const t = STATE_TAG[state] ?? STATE_TAG.enforced;
  return <Tag color={t.color}>{t.label}</Tag>;
};

export const AdminSecurityEgress = () => {
  const summary = useEgressSummary();
  const pending = usePendingEgressRequests();
  const usersQuery = useListQuery<UserRow>({
    resource: "users",
    params: { page: 1, pageSize: 200 },
  });

  const [editingUserID, setEditingUserID] = useState<string | undefined>(undefined);

  const nonAdminUsers = (usersQuery.items ?? []).filter((u: UserRow) => !u.is_admin);

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="Per-user PHP-FPM egress firewall"
        description={
          <Typography.Paragraph style={{ marginBottom: 0 }}>
            Kernel-level packet filter via nftables + cgroup v2 socket match. ENFORCED
            users have outbound traffic dropped if it doesn't hit the default allowlist
            (loopback / DNS / HTTP-S / SMTP submission) or a per-user override.
            LEARNING users have drops logged + counted but allowed (7-day soak before
            auto-flip). OFF disables the filter entirely (break-glass).
          </Typography.Paragraph>
        }
      />

      <Card size="small">
        <Space size="large" wrap>
          <Statistic
            title="Enforced users"
            value={summary.data?.state_counts.enforced ?? 0}
          />
          <Statistic
            title="Learning users"
            value={summary.data?.state_counts.learning ?? 0}
          />
          <Statistic
            title="Off users"
            value={summary.data?.state_counts.off ?? 0}
          />
          <Statistic
            title="Total drops (last tick)"
            value={summary.data?.total_drops ?? 0}
          />
        </Space>
      </Card>

      <Card size="small" title="Per-user policy">
        <Table<UserRow>
          dataSource={nonAdminUsers}
          rowKey="id"
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          loading={usersQuery.isLoading}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<UserRow>
            title="User"
            dataIndex="username"
            render={(_, r) => r.username ?? r.email}
          />
          <Table.Column<UserRow>
            title="Egress state"
            render={(_, r) => <UserStateCell userID={r.id} />}
          />
          <Table.Column<UserRow>
            title="Drops (last tick)"
            render={(_, r) => <UserDropsCell userID={r.id} />}
          />
          <Table.Column<UserRow>
            title="Actions"
            render={(_, r) => (
              <Button size="small" onClick={() => setEditingUserID(r.id)}>
                Edit policy
              </Button>
            )}
          />
        </Table>
      </Card>

      <Card size="small" title={`Pending requests (${pending.data?.total ?? 0})`}>
        <PendingRequestsTable rows={pending.data?.data ?? []} />
      </Card>

      <UserEgressDrawer
        open={!!editingUserID}
        userID={editingUserID}
        onClose={() => setEditingUserID(undefined)}
      />
    </Space>
  );
};

const UserStateCell = ({ userID }: { userID: string }) => {
  const q = useUserEgressPolicy(userID);
  if (q.isLoading) return <span>—</span>;
  if (!q.data) return <Tag>unknown</Tag>;
  return renderStateTag(q.data.state);
};

const UserDropsCell = ({ userID }: { userID: string }) => {
  const q = useUserEgressPolicy(userID);
  if (q.isLoading) return <span>—</span>;
  return <span>{q.data?.drop_count_24h ?? 0}</span>;
};

type UserEgressDrawerProps = {
  open: boolean;
  userID: string | undefined;
  onClose: () => void;
};

type FormValues = {
  state: EgressState;
  allowed_extra: EgressDestination[];
};

const UserEgressDrawer = ({ open, userID, onClose }: UserEgressDrawerProps) => {
  const policy = useUserEgressPolicy(userID);
  const update = useUpdateUserEgress(userID ?? "");
  const [form] = Form.useForm<FormValues>();

  useEffect(() => {
    if (policy.data) {
      form.setFieldsValue({
        state: policy.data.state,
        allowed_extra: policy.data.allowed_extra ?? [],
      });
    }
  }, [policy.data, form]);

  const onFinish = async (values: FormValues) => {
    if (!userID) return;
    try {
      await update.mutateAsync({
        state: values.state,
        allowed_extra: (values.allowed_extra ?? []).map((e) => ({
          ...e,
          protocol: e.protocol ?? "tcp",
        })),
      });
      message.success("Egress policy updated");
      onClose();
    } catch (e) {
      message.error("Failed to update policy");
    }
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      width={640}
      title={`Egress policy${policy.data?.user_id ? ` — ${policy.data.user_id}` : ""}`}
      destroyOnClose
    >
      {policy.isLoading ? (
        <Typography.Text>Loading...</Typography.Text>
      ) : (
        <Form<FormValues> form={form} layout="vertical" onFinish={onFinish}>
          <Form.Item label="State" name="state" rules={[{ required: true }]}>
            <Select options={STATE_OPTIONS} />
          </Form.Item>

          <Typography.Title level={5}>Allowed destinations (extras)</Typography.Title>
          <Typography.Paragraph type="secondary">
            Beyond the default allowlist (loopback / DNS / HTTP-S / SMTP submission).
            Maximum 50 entries.
          </Typography.Paragraph>

          <Form.List name="allowed_extra">
            {(fields, { add, remove }) => (
              <>
                {fields.map((field) => (
                  <Space key={field.key} align="baseline" wrap>
                    <Form.Item
                      label="CIDR"
                      name={[field.name, "cidr"]}
                      rules={[{ required: true, message: "Required" }]}
                    >
                      <Input placeholder="203.0.113.0/24" style={{ width: 200 }} />
                    </Form.Item>
                    <Form.Item label="Port" name={[field.name, "port"]}>
                      <InputNumber min={1} max={65535} style={{ width: 100 }} />
                    </Form.Item>
                    <Form.Item label="Protocol" name={[field.name, "protocol"]}>
                      <Select
                        options={PROTOCOL_OPTIONS}
                        defaultValue="tcp"
                        style={{ width: 100 }}
                      />
                    </Form.Item>
                    <Form.Item label="Comment" name={[field.name, "comment"]}>
                      <Input style={{ width: 200 }} />
                    </Form.Item>
                    <Button danger size="small" onClick={() => remove(field.name)}>
                      Remove
                    </Button>
                  </Space>
                ))}
                <Button onClick={() => add({ protocol: "tcp" })}>+ Add destination</Button>
              </>
            )}
          </Form.List>

          <Form.Item style={{ marginTop: 24 }}>
            <Space>
              <Button type="primary" htmlType="submit" loading={update.isPending}>
                Save
              </Button>
              <Button onClick={onClose}>Cancel</Button>
            </Space>
          </Form.Item>
        </Form>
      )}
    </Drawer>
  );
};

const PendingRequestsTable = ({ rows }: { rows: EgressRequest[] }) => {
  const decide = useDecideEgressRequest();
  if (rows.length === 0) {
    return <Typography.Text type="secondary">No pending requests.</Typography.Text>;
  }
  return (
    <Table<EgressRequest> dataSource={rows} rowKey="id" pagination={false} size="small">
      <Table.Column<EgressRequest> title="User" dataIndex="user_id" />
      <Table.Column<EgressRequest> title="CIDR" dataIndex="cidr" />
      <Table.Column<EgressRequest>
        title="Port"
        render={(_, r) => r.port ?? "—"}
      />
      <Table.Column<EgressRequest> title="Proto" dataIndex="protocol" />
      <Table.Column<EgressRequest>
        title="Reason"
        dataIndex="reason"
        render={(s: string) => (
          <Typography.Text style={{ maxWidth: 280 }} ellipsis={{ tooltip: s }}>
            {s}
          </Typography.Text>
        )}
      />
      <Table.Column<EgressRequest>
        title="Submitted"
        dataIndex="created_at"
        render={(s: string) => new Date(s).toLocaleString()}
      />
      <Table.Column<EgressRequest>
        title="Actions"
        render={(_, r) => (
          <Space>
            <Popconfirm
              title="Approve and add to user's allowlist?"
              onConfirm={() =>
                decide.mutate({ id: r.id, decision: "approve" }, {
                  onSuccess: () => message.success("Request approved"),
                })
              }
            >
              <Button size="small" type="primary">Approve</Button>
            </Popconfirm>
            <Popconfirm
              title="Deny request?"
              onConfirm={() =>
                decide.mutate({ id: r.id, decision: "deny" }, {
                  onSuccess: () => message.info("Request denied"),
                })
              }
            >
              <Button size="small" danger>Deny</Button>
            </Popconfirm>
          </Space>
        )}
      />
    </Table>
  );
};
