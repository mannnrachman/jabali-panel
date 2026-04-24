// PackageCreate — admin form for a new hosting package.
//
// Form.useForm + useCreateMutation; layout matches the old Refine
// version (grid of resource limits + feature quotas + two switches).
import {
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Row,
  Switch,
  Typography,
  message,
} from "antd";
import { CheckOutlined, CloseOutlined } from "@icons";
import { useNavigate } from "react-router";

import { useCreateMutation } from "../../../hooks/useQueries";
import { useDiskQuotaEnabled } from "../../../hooks/useDiskQuotaEnabled";

type PackageCreateInput = {
  name: string;
  disk_quota_mb: number;
  cpu_quota_percent: number;
  memory_limit_mb: number;
  io_read_mbps: number;
  io_write_mbps: number;
  max_tasks: number;
  bandwidth_quota_mb: number;
  max_domains: number;
  max_email_accounts: number;
  max_databases: number;
  max_ftp_accounts: number;
  ssh_enabled: boolean;
  cgi_enabled: boolean;
};

type PackageCreated = { id: string };

export const PackageCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<PackageCreateInput>();
  const { enabled: diskQuotaEnabled } = useDiskQuotaEnabled();
  const createMutation = useCreateMutation<PackageCreated, PackageCreateInput>({
    resource: "packages",
  });

  const handleFinish = async (values: PackageCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("Package created");
      navigate("/jabali-admin/packages");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to create package";
      message.error(msg);
    }
  };

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Create package
      </Typography.Title>
      <Form<PackageCreateInput>
        form={form}
        layout="vertical"
        initialValues={{
          ssh_enabled: false,
          cgi_enabled: false,
          disk_quota_mb: 0,
          cpu_quota_percent: 0,
          memory_limit_mb: 0,
          io_read_mbps: 0,
          io_write_mbps: 0,
          max_tasks: 0,
          bandwidth_quota_mb: 0,
          max_domains: 0,
          max_email_accounts: 0,
          max_databases: 0,
          max_ftp_accounts: 0,
        }}
        onFinish={handleFinish}
      >
        <Form.Item
          label="Name"
          name="name"
          rules={[{ required: true, message: "Package name is required" }]}
        >
          <Input placeholder="e.g., Basic, Professional, Enterprise" />
        </Form.Item>

        <Divider titlePlacement="left">Resource limits</Divider>
        <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
          Enforced per-user via POSIX quota (disk) and cgroups v2
          (cpu/memory/io/tasks). Zero on any field means unlimited.
        </Typography.Paragraph>

        <Row gutter={16}>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Disk Quota (MB)"
              name="disk_quota_mb"
              rules={[{ required: true, message: "Disk quota is required" }]}
              tooltip={
                diskQuotaEnabled
                  ? "Hard limit enforced via setquota(8). 0 = unlimited."
                  : "Disabled — enable POSIX disk quotas in Server Settings → Disk Quotas first."
              }
              extra={
                diskQuotaEnabled
                  ? undefined
                  : "Disabled until disk quotas are enabled in Server Settings."
              }
            >
              <InputNumber min={0} style={{ width: "100%" }} disabled={!diskQuotaEnabled} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="CPU Quota (%)"
              name="cpu_quota_percent"
              tooltip="systemd CPUQuota — 100% = 1 core, 200% = 2 cores. 0 = unlimited."
            >
              <InputNumber min={0} max={10000} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Memory Limit (MB)"
              name="memory_limit_mb"
              tooltip="systemd MemoryMax; MemoryHigh is fixed at 90% of this. 0 = unlimited."
            >
              <InputNumber min={0} max={1048576} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="IO Read Bandwidth (MB/s)"
              name="io_read_mbps"
              tooltip="systemd IOReadBandwidthMax on /. 0 = unlimited."
            >
              <InputNumber min={0} max={10000} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="IO Write Bandwidth (MB/s)"
              name="io_write_mbps"
              tooltip="systemd IOWriteBandwidthMax on /. 0 = unlimited."
            >
              <InputNumber min={0} max={10000} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Max Tasks"
              name="max_tasks"
              tooltip="systemd TasksMax — upper bound on concurrent processes. 0 = unlimited."
            >
              <InputNumber min={0} max={100000} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
        </Row>

        <Divider titlePlacement="left">Feature quotas</Divider>

        <Row gutter={16}>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Bandwidth Quota (MB)"
              name="bandwidth_quota_mb"
              rules={[{ required: true, message: "Bandwidth quota is required" }]}
              tooltip="0 = unlimited"
            >
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Max Domains"
              name="max_domains"
              rules={[{ required: true, message: "Max domains is required" }]}
              tooltip="0 = unlimited"
            >
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Max Email Accounts"
              name="max_email_accounts"
              rules={[
                { required: true, message: "Max email accounts is required" },
              ]}
              tooltip="0 = unlimited"
            >
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Max Databases"
              name="max_databases"
              rules={[{ required: true, message: "Max databases is required" }]}
              tooltip="0 = unlimited"
            >
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Max FTP Accounts"
              name="max_ftp_accounts"
              rules={[
                { required: true, message: "Max FTP accounts is required" },
              ]}
              tooltip="0 = unlimited"
            >
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
          </Col>
        </Row>

        <Divider titlePlacement="left">Features</Divider>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
          <Form.Item
            name="ssh_enabled"
            valuePropName="checked"
            tooltip="Allow SSH access"
            noStyle
          >
            <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
          </Form.Item>
          <Typography.Text>SSH Enabled</Typography.Text>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 24 }}>
          <Form.Item
            name="cgi_enabled"
            valuePropName="checked"
            tooltip="Allow CGI scripts"
            noStyle
          >
            <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
          </Form.Item>
          <Typography.Text>CGI Enabled</Typography.Text>
        </div>

        <Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={createMutation.isPending}
          >
            Save
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
