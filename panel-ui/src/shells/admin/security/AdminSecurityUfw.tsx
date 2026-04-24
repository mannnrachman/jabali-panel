// AdminSecurityUfw — M26 Step 8. Status banner, rules table (hidden
// when firewall disabled), add-rule form, enable/disable buttons with
// typed-YES gate.
import {
  Alert,
  Button,
  Card,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import {
  useAddUfwRule,
  useDeleteUfwRule,
  useUfwStatus,
  useUfwToggle,
  type UfwAction,
  type UfwProto,
  type UfwRule,
} from "../../../hooks/useSecurityUfw";

const ACTION_OPTIONS = [
  { value: "allow", label: "allow" },
  { value: "deny", label: "deny" },
  { value: "reject", label: "reject" },
];

const PROTO_OPTIONS = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
];

// IPv4 / IPv4-CIDR / IPv6 / IPv6-CIDR — agent does authoritative
// net.ParseIP / net.ParseCIDR check.
const IP_OR_CIDR = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;
// Matches "22" or "1000:2000" range.
const PORT_OR_RANGE = /^\d+(:\d+)?$/;

type AddRuleFormValues = {
  action: UfwAction;
  port: string;
  proto: UfwProto;
  from?: string;
};

export const AdminSecurityUfw = () => {
  const status = useUfwStatus();
  const addRule = useAddUfwRule();
  const deleteRule = useDeleteUfwRule();
  const toggle = useUfwToggle();

  const [addForm] = Form.useForm<AddRuleFormValues>();

  const submitAdd = async (values: AddRuleFormValues) => {
    try {
      await addRule.mutateAsync({
        action: values.action,
        port: values.port,
        proto: values.proto,
        from: values.from || undefined,
      });
      message.success(`Rule added`);
      addForm.resetFields();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to add rule");
    }
  };

  const onDelete = async (rule: UfwRule) => {
    try {
      await deleteRule.mutateAsync(rule.num);
      message.success(`Removed rule #${rule.num}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to delete rule");
    }
  };

  const openToggleModal = (enable: boolean) => {
    let typed = "";
    Modal.confirm({
      title: enable ? "Enable firewall" : "Disable firewall",
      content: (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          {enable ? (
            <Alert
              type="warning"
              showIcon
              message="Enabling UFW will activate default-deny incoming. Make sure SSH (22) is in the allow-list."
            />
          ) : (
            <Alert
              type="error"
              showIcon
              message="Disabling UFW DROPS host firewall protection. CrowdSec firewall-bouncer also stops applying rules. Use only for emergency triage."
            />
          )}
          <Typography.Text>
            Type <Typography.Text code>YES</Typography.Text> to confirm:
          </Typography.Text>
          <Input
            placeholder="YES"
            autoComplete="off"
            onChange={(e) => {
              typed = e.target.value;
            }}
          />
        </Space>
      ),
      okText: enable ? "Enable firewall" : "Disable firewall",
      okButtonProps: { danger: !enable },
      onOk: async () => {
        if (typed !== "YES") {
          message.warning('Type "YES" exactly to confirm');
          return Promise.reject(new Error("not confirmed"));
        }
        try {
          await toggle.mutateAsync({ enable });
          message.success(enable ? "Firewall enabled" : "Firewall disabled");
        } catch (e: unknown) {
          message.error(e instanceof Error ? e.message : "Toggle failed");
          throw e;
        }
      },
    });
  };

  const ruleColumns = [
    { title: "#", dataIndex: "num", key: "num", width: 60 },
    {
      title: "Action",
      dataIndex: "action",
      key: "action",
      width: 120,
      render: (a: string) => {
        const lower = a.toLowerCase();
        const color = lower.includes("allow")
          ? "green"
          : lower.includes("deny")
            ? "red"
            : "orange";
        return <Tag color={color}>{a}</Tag>;
      },
    },
    { title: "To", dataIndex: "to", key: "to" },
    { title: "From", dataIndex: "from", key: "from" },
    { title: "Proto", dataIndex: "proto", key: "proto", width: 80 },
    {
      title: "",
      key: "delete",
      width: 90,
      render: (_: unknown, row: UfwRule) => (
        <Popconfirm
          title="Delete rule"
          description={`Delete rule #${row.num} (${row.action} ${row.to})? Existing connections continue; new ones are subject to the next-matching rule.`}
          okText="Delete"
          okButtonProps={{ danger: true }}
          cancelText="Cancel"
          onConfirm={() => onDelete(row)}
        >
          <Button danger size="small">
            Delete
          </Button>
        </Popconfirm>
      ),
    },
  ];

  const active = status.data?.active ?? false;

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      {!status.isLoading && !active && (
        <Alert
          type="error"
          showIcon
          message="Firewall DISABLED"
          description="UFW is not active. Rules below (if any) are not enforced. CrowdSec firewall-bouncer also has no effect until UFW is enabled."
          action={
            <Button type="primary" onClick={() => openToggleModal(true)}>
              Enable firewall
            </Button>
          }
        />
      )}

      <Card size="small" title="Status">
        <Space wrap>
          {active ? <Tag color="green">active</Tag> : <Tag color="red">inactive</Tag>}
          {status.data?.default_in && (
            <Tag>
              default in: <strong>{status.data.default_in}</strong>
            </Tag>
          )}
          {status.data?.default_out && (
            <Tag>
              default out: <strong>{status.data.default_out}</strong>
            </Tag>
          )}
          {active ? (
            <Popconfirm
              title="Disable firewall?"
              description="This drops the host firewall. CrowdSec firewall-bouncer stops applying rules. Confirm again in the next dialog."
              okText="Continue"
              okButtonProps={{ danger: true }}
              onConfirm={() => openToggleModal(false)}
            >
              <Button danger size="small">
                Disable firewall
              </Button>
            </Popconfirm>
          ) : (
            <Button type="primary" size="small" onClick={() => openToggleModal(true)}>
              Enable firewall
            </Button>
          )}
        </Space>
      </Card>

      {active ? (
        <Card size="small" title="Rules">
          <Table<UfwRule>
            rowKey="num"
            dataSource={status.data?.rules ?? []}
            columns={ruleColumns}
            loading={status.isLoading}
            pagination={false}
            size="small"
            locale={{ emptyText: <Empty description="No rules" /> }}
            scroll={{ x: "max-content" }}
          />
        </Card>
      ) : (
        <Alert
          type="info"
          showIcon
          message="Rules hidden — firewall disabled"
          description="Enable the firewall to view and manage rules."
        />
      )}

      <Card size="small" title="Add rule">
        <Form
          form={addForm}
          layout="inline"
          onFinish={submitAdd}
          initialValues={{ action: "allow", proto: "tcp" }}
        >
          <Form.Item name="action" rules={[{ required: true }]}>
            <Select options={ACTION_OPTIONS} style={{ minWidth: 100 }} />
          </Form.Item>
          <Form.Item
            name="port"
            label="Port"
            rules={[
              { required: true, message: "Required" },
              { pattern: PORT_OR_RANGE, message: 'Number or "lo:hi" range' },
            ]}
          >
            <Input placeholder="9999 or 1000:2000" autoComplete="off" />
          </Form.Item>
          <Form.Item name="proto" rules={[{ required: true }]}>
            <Select options={PROTO_OPTIONS} style={{ minWidth: 80 }} />
          </Form.Item>
          <Form.Item
            name="from"
            label="From (optional)"
            rules={[{ pattern: IP_OR_CIDR, message: "IP or CIDR" }]}
          >
            <Input placeholder="203.0.113.0/24" autoComplete="off" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={addRule.isPending}>
              Add rule
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </Space>
  );
};
