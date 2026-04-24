// AdminSecurityCrowdsec — M26 Step 7. Four cards: metrics, active
// decisions (with add modal + delete popconfirm), bouncers (read-only),
// hub items (read-only). Polls metrics + status every 30s.
import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tag,
  Typography,
} from "antd";
import { useState } from "react";

import {
  useAddCrowdsecDecision,
  useCrowdsecBouncers,
  useCrowdsecDecisions,
  useCrowdsecHub,
  useCrowdsecMetrics,
  useCrowdsecStatus,
  useDeleteCrowdsecDecision,
  type CrowdsecDecision,
  type CrowdsecScope,
} from "../../../hooks/useSecurityCrowdsec";

const SCOPE_OPTIONS: Array<{ value: CrowdsecScope | "all"; label: string }> = [
  { value: "all", label: "All scopes" },
  { value: "ip", label: "IP" },
  { value: "range", label: "Range (CIDR)" },
  { value: "country", label: "Country" },
  { value: "as", label: "AS" },
];

// IPv4 / IPv4-CIDR / IPv6 / IPv6-CIDR — keep simple; agent does the
// authoritative net.ParseIP / net.ParseCIDR validation.
const IP_OR_CIDR = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;
// CrowdSec accepts Go time.ParseDuration: 4h, 1h30m, 30m, 1d (custom).
const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d))+$/;

export const AdminSecurityCrowdsec = () => {
  const status = useCrowdsecStatus();
  const metrics = useCrowdsecMetrics();
  const [scope, setScope] = useState<CrowdsecScope | "all">("all");
  const decisions = useCrowdsecDecisions(scope === "all" ? undefined : scope);
  const bouncers = useCrowdsecBouncers();
  const hub = useCrowdsecHub();
  const addDecision = useAddCrowdsecDecision();
  const deleteDecision = useDeleteCrowdsecDecision();

  const [addOpen, setAddOpen] = useState(false);
  const [addForm] = Form.useForm<{ ip: string; duration: string; reason: string }>();

  const submitAdd = async (values: { ip: string; duration: string; reason: string }) => {
    try {
      await addDecision.mutateAsync(values);
      message.success(`Decision added for ${values.ip}`);
      setAddOpen(false);
      addForm.resetFields();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to add decision");
    }
  };

  const onDeleteDecision = async (row: CrowdsecDecision) => {
    try {
      await deleteDecision.mutateAsync(row.id);
      message.success(`Removed ban on ${row.ip}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to remove decision");
    }
  };

  const decisionColumns = [
    { title: "IP", dataIndex: "ip", key: "ip" },
    { title: "Scenario", dataIndex: "scenario", key: "scenario" },
    { title: "Reason", dataIndex: "reason", key: "reason" },
    {
      title: "Until",
      dataIndex: "until",
      key: "until",
      render: (s: string) => (s ? new Date(s).toLocaleString() : "—"),
    },
    {
      title: "",
      key: "delete",
      width: 90,
      render: (_: unknown, row: CrowdsecDecision) => (
        <Popconfirm
          title="Remove ban"
          description={`Remove the ban on ${row.ip}? Traffic will resume immediately.`}
          okText="Remove"
          okButtonProps={{ danger: true }}
          cancelText="Cancel"
          onConfirm={() => onDeleteDecision(row)}
        >
          <Button danger size="small">
            Delete
          </Button>
        </Popconfirm>
      ),
    },
  ];

  const bouncerColumns = [
    { title: "Name", dataIndex: "name", key: "name" },
    { title: "Type", dataIndex: "type", key: "type" },
    {
      title: "Last pull",
      dataIndex: "last_pull",
      key: "last_pull",
      render: (s: string) => (s ? new Date(s).toLocaleString() : "—"),
    },
    {
      title: "Status",
      dataIndex: "revoked",
      key: "revoked",
      render: (revoked: boolean) =>
        revoked ? <Tag color="red">revoked</Tag> : <Tag color="green">active</Tag>,
    },
  ];

  const hubColumns = [
    { title: "Name", dataIndex: "name", key: "name" },
    { title: "Type", dataIndex: "type", key: "type" },
    {
      title: "Installed",
      dataIndex: "installed",
      key: "installed",
      render: (v: boolean) => (v ? <Tag color="blue">yes</Tag> : <Tag>no</Tag>),
    },
    {
      title: "Enabled",
      dataIndex: "enabled",
      key: "enabled",
      render: (v: boolean) => (v ? <Tag color="green">yes</Tag> : <Tag>no</Tag>),
    },
  ];

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card size="small" title="CrowdSec status">
        <Row gutter={16} style={{ rowGap: 8 }}>
          <Statistic
            title="Service"
            value={status.data?.running ? "running" : "down"}
            valueStyle={{ color: status.data?.running ? "#3f8600" : "#cf1322", fontSize: 18 }}
          />
          <Statistic
            title="LAPI"
            value={status.data?.lapi_reachable ? "reachable" : "unreachable"}
            valueStyle={{
              color: status.data?.lapi_reachable ? "#3f8600" : "#cf1322",
              fontSize: 18,
              marginLeft: 24,
            }}
          />
          {status.data?.version && (
            <Statistic
              title="Version"
              value={status.data.version}
              valueStyle={{ fontSize: 18, marginLeft: 24 }}
            />
          )}
        </Row>
      </Card>

      <Card size="small" title="Metrics">
        {metrics.isLoading ? (
          <Typography.Text type="secondary">Loading…</Typography.Text>
        ) : (
          <Row gutter={32} style={{ rowGap: 8 }}>
            <Statistic title="Parsed events" value={metrics.data?.parsed ?? 0} />
            <Statistic
              title="Unparsed"
              value={metrics.data?.unparsed ?? 0}
              style={{ marginLeft: 24 }}
            />
            <Statistic
              title="Buckets fired"
              value={metrics.data?.buckets ?? 0}
              style={{ marginLeft: 24 }}
            />
            <Statistic
              title="Active decisions"
              value={metrics.data?.decisions_active ?? 0}
              style={{ marginLeft: 24 }}
            />
            <Statistic
              title="Total alerts"
              value={metrics.data?.alerts_total ?? 0}
              style={{ marginLeft: 24 }}
            />
          </Row>
        )}
      </Card>

      <Card
        size="small"
        title="Active decisions"
        extra={
          <Space>
            <Select
              size="small"
              value={scope}
              style={{ minWidth: 160 }}
              options={SCOPE_OPTIONS}
              onChange={(v) => setScope(v)}
            />
            <Button type="primary" size="small" onClick={() => setAddOpen(true)}>
              Add decision
            </Button>
          </Space>
        }
      >
        <Table<CrowdsecDecision>
          rowKey="id"
          dataSource={decisions.data ?? []}
          columns={decisionColumns}
          loading={decisions.isLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="No active decisions" /> }}
          scroll={{ x: "max-content" }}
        />
      </Card>

      <Card size="small" title="Bouncers">
        <Table
          rowKey="name"
          dataSource={bouncers.data ?? []}
          columns={bouncerColumns}
          loading={bouncers.isLoading}
          pagination={false}
          locale={{ emptyText: <Empty description="No bouncers registered" /> }}
          scroll={{ x: "max-content" }}
        />
      </Card>

      <Card size="small" title="Hub scenarios">
        <Table
          rowKey={(r) => `${r.type}:${r.name}`}
          dataSource={hub.data ?? []}
          columns={hubColumns}
          loading={hub.isLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="No hub items" /> }}
          scroll={{ x: "max-content" }}
        />
      </Card>

      <Modal
        open={addOpen}
        title="Add CrowdSec decision (manual ban)"
        onCancel={() => setAddOpen(false)}
        onOk={() => addForm.submit()}
        okText="Add ban"
        confirmLoading={addDecision.isPending}
      >
        <Form form={addForm} layout="vertical" onFinish={submitAdd}>
          <Form.Item
            name="ip"
            label="IP or CIDR"
            rules={[
              { required: true, message: "IP or CIDR required" },
              {
                pattern: IP_OR_CIDR,
                message: "Must be a valid IPv4/IPv6 address or CIDR (e.g. 203.0.113.7 or 203.0.113.0/24)",
              },
            ]}
          >
            <Input placeholder="203.0.113.7" autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="duration"
            label="Duration"
            rules={[
              { required: true, message: "Duration required" },
              { pattern: DURATION, message: 'Use Go duration syntax: "30m", "4h", "1h30m"' },
            ]}
          >
            <Input placeholder="4h" autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="reason"
            label="Reason"
            rules={[
              { required: true, message: "Reason required" },
              { min: 3, max: 200, message: "3..200 characters" },
            ]}
          >
            <Input placeholder="manual ban — port-scan from this IP" autoComplete="off" />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  );
};
