import { useForm } from "@refinedev/antd";
import { Create } from "@refinedev/antd";
import { Form, Input, InputNumber, Switch } from "antd";

type PackageCreateInput = {
  name: string;
  disk_quota_mb: number;
  bandwidth_quota_mb: number;
  max_domains: number;
  max_email_accounts: number;
  max_databases: number;
  max_ftp_accounts: number;
  ssh_enabled: boolean;
  cgi_enabled: boolean;
};

export const PackageCreate = () => {
  const { formProps, saveButtonProps } = useForm<PackageCreateInput>({
    resource: "packages",
    action: "create",
  });

  return (
    <Create saveButtonProps={saveButtonProps}>
      <Form
        {...formProps}
        layout="vertical"
        initialValues={{
          ssh_enabled: false,
          cgi_enabled: false,
          disk_quota_mb: 0,
          bandwidth_quota_mb: 0,
          max_domains: 0,
          max_email_accounts: 0,
          max_databases: 0,
          max_ftp_accounts: 0,
        }}
      >
        <Form.Item
          label="Name"
          name="name"
          rules={[{ required: true, message: "Package name is required" }]}
        >
          <Input placeholder="e.g., Basic, Professional, Enterprise" />
        </Form.Item>

        <Form.Item
          label="Disk Quota (MB)"
          name="disk_quota_mb"
          rules={[{ required: true, message: "Disk quota is required" }]}
          tooltip="0 = unlimited"
        >
          <InputNumber min={0} />
        </Form.Item>

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
    </Create>
  );
};
