import { useForm } from "@refinedev/antd";
import { Edit } from "@refinedev/antd";
import { Col, Divider, Form, Input, InputNumber, Row, Switch, Typography } from "antd";

type PackageEditInput = {
  name: string;
  disk_quota_mb: number;
  // M18 — per-user resource limits. 0 means unlimited on every field.
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

export const PackageEdit = () => {
  const { formProps, saveButtonProps } = useForm<PackageEditInput>({
    resource: "packages",
    action: "edit",
  });

  return (
    <Edit saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
        <Form.Item
          label="Name"
          name="name"
          rules={[{ required: true, message: "Package name is required" }]}
        >
          <Input placeholder="e.g., Basic, Professional, Enterprise" />
        </Form.Item>

        <Divider titlePlacement="left">Resource limits</Divider>
        <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
          Enforced per-user via POSIX quota (disk) and cgroups v2 (cpu/memory/io/tasks).
          Zero on any field means unlimited.
        </Typography.Paragraph>

        {/* Resource limits — 3 cols on md+, 2 on sm, 1 stacked on xs.
            Mirrors the same layout in PackageCreate.tsx. */}
        <Row gutter={16}>
          <Col xs={24} sm={12} md={8}>
            <Form.Item
              label="Disk Quota (MB)"
              name="disk_quota_mb"
              rules={[{ required: true, message: "Disk quota is required" }]}
              tooltip="Hard limit enforced via setquota(8). 0 = unlimited."
            >
              <InputNumber min={0} style={{ width: "100%" }} />
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

        <Form.Item
          label="Bandwidth Quota (MB)"
          name="bandwidth_quota_mb"
          rules={[{ required: true, message: "Bandwidth quota is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

        <Form.Item
          label="Max Domains"
          name="max_domains"
          rules={[{ required: true, message: "Max domains is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

        <Form.Item
          label="Max Email Accounts"
          name="max_email_accounts"
          rules={[{ required: true, message: "Max email accounts is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

        <Form.Item
          label="Max Databases"
          name="max_databases"
          rules={[{ required: true, message: "Max databases is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

        <Form.Item
          label="Max FTP Accounts"
          name="max_ftp_accounts"
          rules={[{ required: true, message: "Max FTP accounts is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

        <Divider titlePlacement="left">Features</Divider>

        <Form.Item
          label="SSH Enabled"
          name="ssh_enabled"
          valuePropName="checked"
          tooltip="Allow SSH access"
        >
          <Switch />
        </Form.Item>

        <Form.Item
          label="CGI Enabled"
          name="cgi_enabled"
          valuePropName="checked"
          tooltip="Allow CGI scripts"
        >
          <Switch />
        </Form.Item>
      </Form>
    </Edit>
  );
};
