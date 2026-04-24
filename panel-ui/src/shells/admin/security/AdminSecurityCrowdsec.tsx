// AdminSecurityCrowdsec — M26 Step 7. Four cards: metrics, active
// decisions (with add Drawer + delete Popconfirm), bouncers (read-only),
// hub items (read-only). Polls metrics + status every 30s.
//
// Conventions: per docs/CONVENTIONS.md the "create" affordance is a
// Drawer (not a Modal), Tables consume <Table.Column> children (not a
// columns prop), and Statistic rows lay out via Row gutter rather than
// inline marginLeft. Hooks stay direct useQuery (not useTableURL) —
// these endpoints are not the standard {data,total,page,page_size}
// list shape; they're agent passthroughs.
import {
  Button,
  Card,
  Col,
  Drawer,
  Empty,
  Form,
  Grid,
  Input,
  message,
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

type AddDecisionFormValues = { ip: string; duration: string; reason: string };

const fmtTime = (s?: string): string => (s ? new Date(s).toLocaleString() : "—");

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
  const [addForm] = Form.useForm<AddDecisionFormValues>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;

  const submitAdd = async (values: AddDecisionFormValues) => {
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

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card size="small" title="CrowdSec status">
        <Row gutter={[32, 16]}>
          <Col>
            <Statistic
              title="Service"
              value={status.data?.running ? "running" : "down"}
              valueStyle={{
                color: status.data?.running ? "#3f8600" : "#cf1322",
              }}
            />
          </Col>
          <Col>
            <Statistic
              title="LAPI"
              value={status.data?.lapi_reachable ? "reachable" : "unreachable"}
              valueStyle={{
                color: status.data?.lapi_reachable ? "#3f8600" : "#cf1322",
              }}
            />
          </Col>
          {status.data?.version && (
            <Col>
              <Statistic title="Version" value={status.data.version} />
            </Col>
          )}
        </Row>
      </Card>

      <Card size="small" title="Metrics">
        {metrics.isLoading ? (
          <Typography.Text type="secondary">Loading…</Typography.Text>
        ) : (
          <Row gutter={[32, 16]}>
            <Col>
              <Statistic title="Parsed events" value={metrics.data?.parsed ?? 0} />
            </Col>
            <Col>
              <Statistic title="Unparsed" value={metrics.data?.unparsed ?? 0} />
            </Col>
            <Col>
              <Statistic title="Buckets fired" value={metrics.data?.buckets ?? 0} />
            </Col>
            <Col>
              <Statistic title="Active decisions" value={metrics.data?.decisions_active ?? 0} />
            </Col>
            <Col>
              <Statistic title="Total alerts" value={metrics.data?.alerts_total ?? 0} />
            </Col>
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
          loading={decisions.isLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="No active decisions" /> }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<CrowdsecDecision> dataIndex="ip" title="IP" key="ip" />
          <Table.Column<CrowdsecDecision> dataIndex="scenario" title="Scenario" key="scenario" />
          <Table.Column<CrowdsecDecision> dataIndex="reason" title="Reason" key="reason" />
          <Table.Column<CrowdsecDecision>
            dataIndex="until"
            title="Until"
            key="until"
            render={(s: string) => fmtTime(s)}
          />
          <Table.Column<CrowdsecDecision>
            title=""
            key="delete"
            width={90}
            render={(_, row) => (
              <Popconfirm
                title="Remove ban"
                description={`Remove the ban on ${row.ip}? Traffic will resume immediately.`}
                okText="Remove"
                okButtonProps={{ danger: true }}
                cancelText="Cancel"
                onConfirm={() => onDeleteDecision(row)}
              >
                <Button danger type="text" size="small">
                  Delete
                </Button>
              </Popconfirm>
            )}
          />
        </Table>
      </Card>

      <Card size="small" title="Bouncers">
        <Table
          rowKey="name"
          dataSource={bouncers.data ?? []}
          loading={bouncers.isLoading}
          pagination={false}
          locale={{ emptyText: <Empty description="No bouncers registered" /> }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column dataIndex="name" title="Name" key="name" />
          <Table.Column dataIndex="type" title="Type" key="type" />
          <Table.Column
            dataIndex="last_pull"
            title="Last pull"
            key="last_pull"
            render={(s: string) => fmtTime(s)}
          />
          <Table.Column
            dataIndex="revoked"
            title="Status"
            key="revoked"
            render={(revoked: boolean) =>
              revoked ? <Tag color="red">revoked</Tag> : <Tag color="green">active</Tag>
            }
          />
        </Table>
      </Card>

      <Card size="small" title="Hub scenarios">
        <Table
          rowKey={(r: { type: string; name: string }) => `${r.type}:${r.name}`}
          dataSource={hub.data ?? []}
          loading={hub.isLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty description="No hub items" /> }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column dataIndex="name" title="Name" key="name" />
          <Table.Column dataIndex="type" title="Type" key="type" />
          <Table.Column
            dataIndex="installed"
            title="Installed"
            key="installed"
            render={(v: boolean) => (v ? <Tag color="blue">yes</Tag> : <Tag>no</Tag>)}
          />
          <Table.Column
            dataIndex="enabled"
            title="Enabled"
            key="enabled"
            render={(v: boolean) => (v ? <Tag color="green">yes</Tag> : <Tag>no</Tag>)}
          />
        </Table>
      </Card>

      <Drawer
        title="Add CrowdSec decision (manual ban)"
        open={addOpen}
        onClose={() => setAddOpen(false)}
        width={isDesktop ? 520 : undefined}
        placement="right"
        destroyOnClose
        extra={
          <Space>
            <Button onClick={() => setAddOpen(false)}>Cancel</Button>
            <Button
              type="primary"
              danger
              loading={addDecision.isPending}
              onClick={() => addForm.submit()}
            >
              Add ban
            </Button>
          </Space>
        }
      >
        <Form<AddDecisionFormValues> form={addForm} layout="vertical" onFinish={submitAdd}>
          <Form.Item
            name="ip"
            label="IP or CIDR"
            rules={[
              { required: true, message: "IP or CIDR required" },
              {
                pattern: IP_OR_CIDR,
                message:
                  "Must be a valid IPv4/IPv6 address or CIDR (e.g. 203.0.113.7 or 203.0.113.0/24)",
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
      </Drawer>
    </Space>
  );
};
