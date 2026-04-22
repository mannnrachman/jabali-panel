import { useEffect, useState } from "react";
import { CheckOutlined, CloseOutlined, SaveOutlined, WarningOutlined } from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Switch,
  Typography,
  notification,
} from "antd";

// Post-M21 notify shim: matches the Refine useNotification().open
// contract (`{ type, message, description }`) so callers don't have
// to change. Forwards to AntD's native `notification.open`.
type NotifyInput = {
  type?: "success" | "error" | "warning" | "info";
  message: string;
  description?: React.ReactNode;
};
function useNotify() {
  return (input: NotifyInput) => {
    notification.open({
      message: input.message,
      description: input.description,
      type: input.type,
    });
  };
}

import { apiClient } from "../../../apiClient";
import { DNSResolversCard } from "./DNSResolversCard";

type ServerSettings = {
  id: number;
  hostname: string;
  public_ipv4: string;
  public_ipv6: string;
  ns1_name: string;
  ns1_ipv4: string;
  ns2_name: string;
  ns2_ipv4: string;
  admin_email: string;
  timezone: string;
  ssh_port: number;
  ssh_password_auth: boolean;
  ssh_user_password_auth: boolean;
  updated_at: string;
};

// notifyError narrows axios-style errors to a friendly message.
type NotifyFn = ReturnType<typeof useNotify>;
function notifyError(notify: NotifyFn, title: string, err: unknown) {
  const e = err as { response?: { data?: { detail?: string } }; message?: string };
  notify({
    type: "error",
    message: title,
    description: e.response?.data?.detail ?? e.message ?? "Unknown error",
  });
}

// GeneralSettingsTab — Identity + Server Time + SSH Access. Owns its own
// form + Save button; PATCH /admin/settings supports partial updates so
// sending only this tab's fields doesn't disturb DNS settings.
const GeneralSettingsTab = () => {
  const [form] = Form.useForm<ServerSettings>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [originalHostname, setOriginalHostname] = useState("");
  const [originalSSHPort, setOriginalSSHPort] = useState(22);
  const [originalSSHPasswordAuth, setOriginalSSHPasswordAuth] = useState(false);
  const [originalSSHUserPasswordAuth, setOriginalSSHUserPasswordAuth] = useState(false);
  const notify = useNotify();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await apiClient.get<ServerSettings>("/admin/settings");
        if (cancelled) return;
        form.setFieldsValue(resp.data);
        setOriginalHostname(resp.data.hostname);
        setOriginalSSHPort(resp.data.ssh_port || 22);
        setOriginalSSHPasswordAuth(resp.data.ssh_password_auth || false);
        setOriginalSSHUserPasswordAuth(resp.data.ssh_user_password_auth || false);
      } catch (err) {
        notifyError(notify, "Failed to load settings", err);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSubmit = async (values: ServerSettings) => {
    setSaving(true);
    try {
      const resp = await apiClient.patch<ServerSettings>("/admin/settings", {
        hostname: values.hostname,
        public_ipv4: values.public_ipv4,
        public_ipv6: values.public_ipv6 || "",
        admin_email: values.admin_email || "",
        timezone: values.timezone || "",
        ssh_port: values.ssh_port || 22,
        ssh_password_auth: values.ssh_password_auth || false,
        ssh_user_password_auth: values.ssh_user_password_auth || false,
      });
      notify({ type: "success", message: "Settings saved" });
      form.setFieldsValue(resp.data);
      setOriginalHostname(resp.data.hostname);
      setOriginalSSHPort(resp.data.ssh_port || 22);
      setOriginalSSHPasswordAuth(resp.data.ssh_password_auth || false);
      setOriginalSSHUserPasswordAuth(resp.data.ssh_user_password_auth || false);
    } catch (err) {
      notifyError(notify, "Failed to save", err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form
      form={form}
      layout="vertical"
      onFinish={handleSubmit}
      disabled={loading}
    >
      <Form.Item
        shouldUpdate={(prev, cur) => prev.hostname !== cur.hostname}
        noStyle
      >
        {({ getFieldValue }) => {
          const current = getFieldValue("hostname");
          if (!originalHostname || current === originalHostname) return null;
          return (
            <Alert
              type="warning"
              showIcon
              icon={<WarningOutlined />}
              title="Hostname change"
              description={
                <>
                  Changing the hostname updates the OS hostname and the
                  default nameserver names. <b>Any existing registrar NS
                  delegations using the old hostname will break</b> — all
                  hosted domain owners must update their registrar records
                  to point at <code>ns1.{current}</code> /{" "}
                  <code>ns2.{current}</code>.
                </>
              }
              style={{ marginBottom: 16 }}
            />
          );
        }}
      </Form.Item>

      <Card title="Identity" style={{ marginBottom: 16 }}>
        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item
              label="Hostname"
              name="hostname"
              rules={[
                { required: true, message: "Hostname required" },
                {
                  pattern:
                    /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/,
                  message: "Invalid hostname",
                },
              ]}
              extra="Fully-qualified name for this server (e.g. panel.example.com)."
            >
              <Input placeholder="panel.example.com" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              label="Admin email"
              name="admin_email"
              rules={[{ type: "email", message: "Invalid email" }]}
              extra="Used as the registration email for Let's Encrypt / ACME. Required before issuing SSL certificates."
            >
              <Input placeholder="admin@example.com" />
            </Form.Item>
          </Col>
        </Row>

        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item
              label="Public IPv4"
              name="public_ipv4"
              rules={[
                { required: true, message: "IPv4 required" },
                {
                  pattern: /^[0-9]{1,3}(\.[0-9]{1,3}){3}$/,
                  message: "Invalid IPv4",
                },
              ]}
            >
              <Input placeholder="203.0.113.5" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              label="Public IPv6 (optional)"
              name="public_ipv6"
              rules={[
                {
                  pattern: /^$|^[0-9a-fA-F:]+$/,
                  message: "Invalid IPv6",
                },
              ]}
            >
              <Input placeholder="2001:db8::1" />
            </Form.Item>
          </Col>
        </Row>
      </Card>

      <Card title="Server Time" style={{ marginBottom: 16 }}>
        <Form.Item
          label="Timezone"
          name="timezone"
          rules={[{ required: false }]}
          extra="Select your server's timezone. Changes take effect immediately."
        >
          <Select
            placeholder="Select timezone"
            allowClear
            showSearch
            optionFilterProp="children"
            filterOption={(input, option) =>
              (option?.label ?? "").toLowerCase().includes(input.toLowerCase())
            }
            options={Array.from(Intl.supportedValuesOf("timeZone")).map((tz) => ({
              label: tz,
              value: tz,
            }))}
          />
        </Form.Item>
      </Card>

      <Card title="SSH Access" style={{ marginBottom: 16 }}>
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          Configure SSH port and authentication method. Changes are applied
          immediately and are reversible.
        </Typography.Paragraph>

        <Row gutter={16}>
          <Col span={24}>
            <Form.Item
              label="SSH Port"
              name="ssh_port"
              rules={[
                { required: true, message: "SSH port required" },
                {
                  type: "number",
                  min: 1,
                  max: 65535,
                  message: "Port must be between 1 and 65535",
                },
              ]}
              extra="Standard SSH port is 22. Change to reduce automated attack attempts."
            >
              <InputNumber min={1} max={65535} style={{ width: 200 }} />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} md={12}>
            <div style={{ marginBottom: 24 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
                <Form.Item name="ssh_password_auth" valuePropName="checked" noStyle>
                  <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
                </Form.Item>
                <Typography.Text>Root Password Authentication</Typography.Text>
              </div>
              <Typography.Text type="secondary">
                Allow root and other non-hosting users to log in with a password. Key-based authentication is always available.
              </Typography.Text>
            </div>
          </Col>
          <Col xs={24} md={12}>
            <div style={{ marginBottom: 24 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
                <Form.Item name="ssh_user_password_auth" valuePropName="checked" noStyle>
                  <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
                </Form.Item>
                <Typography.Text>User Password Authentication</Typography.Text>
              </div>
              <Typography.Text type="secondary">
                Allow hosting users (jabali-sftp group) to authenticate with a password. They are still SFTP-only — no shell.
              </Typography.Text>
            </div>
          </Col>
        </Row>
      </Card>

      <Space>
        <Button
          type="primary"
          icon={<SaveOutlined />}
          loading={saving}
          htmlType="submit"
          onClick={() => {
            const currentSSHPort = form.getFieldValue("ssh_port") || 22;
            const currentSSHPasswordAuth =
              form.getFieldValue("ssh_password_auth") || false;
            const currentSSHUserPasswordAuth =
              form.getFieldValue("ssh_user_password_auth") || false;

            const sshPortChanged = currentSSHPort !== originalSSHPort;
            const sshAuthChanged =
              currentSSHPasswordAuth !== originalSSHPasswordAuth;
            const sshUserAuthChanged =
              currentSSHUserPasswordAuth !== originalSSHUserPasswordAuth;

            if (sshPortChanged || sshAuthChanged || sshUserAuthChanged) {
              Modal.confirm({
                title: "Confirm SSH Configuration Change",
                content: (
                  <Alert
                    type="warning"
                    showIcon
                    title="Potential Lockout Risk"
                    description={
                      <>
                        Changing SSH settings may affect your ability to
                        connect remotely. <b>Make sure you have:</b>
                        <ul>
                          <li>Verified the new SSH port or authentication method works</li>
                          <li>An alternative way to access the server if the changes break connectivity</li>
                          <li>The ability to roll back quickly if needed</li>
                        </ul>
                      </>
                    }
                    style={{ marginBottom: 12 }}
                  />
                ),
                okText: "Apply Changes",
                okType: "primary",
                cancelText: "Cancel",
                icon: <WarningOutlined />,
                onOk() {
                  form.submit();
                },
              });
            } else {
              form.submit();
            }
          }}
        >
          Save Settings
        </Button>
      </Space>
    </Form>
  );
};

// DNSSettingsTab — DNS Nameservers card. Independent form + Save button;
// PATCH /admin/settings only writes the ns* fields it sends so this
// doesn't clobber identity/SSH/timezone settings.
const DNSSettingsTab = () => {
  const [form] = Form.useForm<ServerSettings>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const notify = useNotify();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await apiClient.get<ServerSettings>("/admin/settings");
        if (cancelled) return;
        form.setFieldsValue(resp.data);
      } catch (err) {
        notifyError(notify, "Failed to load settings", err);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSubmit = async (values: ServerSettings) => {
    setSaving(true);
    try {
      const resp = await apiClient.patch<ServerSettings>("/admin/settings", {
        ns1_name: values.ns1_name || "",
        ns1_ipv4: values.ns1_ipv4 || "",
        ns2_name: values.ns2_name || "",
        ns2_ipv4: values.ns2_ipv4 || "",
      });
      notify({ type: "success", message: "DNS nameservers saved" });
      form.setFieldsValue(resp.data);
    } catch (err) {
      notifyError(notify, "Failed to save", err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <DNSResolversCard />

      <Form
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        disabled={loading}
      >
        <Card title="DNS Nameservers" style={{ marginBottom: 16 }}>
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          These are the names and addresses your customers will set at their
          registrar. You typically run ns1 on this server and ns2 on a
          separate box. ns2 is optional at first — fill it in once you have
          a second nameserver provisioned.
        </Typography.Paragraph>

        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item label="ns1 hostname" name="ns1_name">
              <Input placeholder="ns1.panel.example.com" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="ns1 IPv4" name="ns1_ipv4">
              <Input placeholder="203.0.113.5" />
            </Form.Item>
          </Col>
        </Row>

        <Divider titlePlacement="left" plain>
          Secondary (optional)
        </Divider>

        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item label="ns2 hostname" name="ns2_name">
              <Input placeholder="ns2.panel.example.com" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="ns2 IPv4" name="ns2_ipv4">
              <Input placeholder="" />
            </Form.Item>
          </Col>
        </Row>
      </Card>

        <Space>
          <Button
            type="primary"
            icon={<SaveOutlined />}
            loading={saving}
            htmlType="submit"
          >
            Save DNS Settings
          </Button>
        </Space>
      </Form>
    </>
  );
};

export const ServerSettingsPage = () => {
  const [activeTab, setActiveTab] = useState<"general" | "dns">("general");

  return (
    <div style={{ maxWidth: 960 }}>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Server Settings
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Server identity, DNS nameserver names, and administrative contact info.
      </Typography.Paragraph>

      {/* Card.tabList renders the tab strip attached to the card body —
          each tab owns an independent form, so unsaved edits in the
          inactive tab are lost on switch (mirrors the Users page pattern). */}
      <Card
        tabList={[
          { key: "general", tab: "General" },
          { key: "dns", tab: "DNS" },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as "general" | "dns")}
      >
        {activeTab === "general" ? <GeneralSettingsTab /> : <DNSSettingsTab />}
      </Card>
    </div>
  );
};
