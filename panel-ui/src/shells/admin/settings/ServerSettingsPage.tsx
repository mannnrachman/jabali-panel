import { useEffect, useState } from "react";
import {
  BgColorsOutlined,
  CheckOutlined,
  CloseOutlined,
  GlobalOutlined,
  HddOutlined,
  MailOutlined,
  SaveOutlined,
  SettingOutlined,
  WarningOutlined,
} from "@icons";
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
import { BrandingCard } from "./BrandingCard";
import { DNSResolversCard } from "./DNSResolversCard";
import { EmailCard } from "./EmailCard";
import { PageTemplatesCard } from "./PageTemplatesCard";
import { PanelSSLCard } from "./PanelSSLCard";

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
  ssh_sandbox_mode: "bubblewrap" | "nspawn";
  default_nspawn_image_version: string;
  disk_quota_enabled: boolean;
  upload_max_size_mb: number;
  updated_at: string;
};

type NspawnImage = {
  name: string;
  manifest?: string;
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
  const [nspawnImages, setNspawnImages] = useState<NspawnImage[]>([]);
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
        // Fetch nspawn images for the default-image dropdown.
        try {
          const imgResp = await apiClient.get<{ images: NspawnImage[] }>(
            "/admin/system/nspawn-images",
          );
          if (!cancelled) setNspawnImages(imgResp.data.images || []);
        } catch {
          // Empty list is fine — admin sees a placeholder + warning.
        }
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
        ssh_sandbox_mode: values.ssh_sandbox_mode || "bubblewrap",
        default_nspawn_image_version:
          values.default_nspawn_image_version || "debian-13-v1",
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

        <Divider style={{ margin: "8px 0 16px" }}>Shell Sandbox</Divider>

        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          SSH-shell users land in a sandbox. Bubblewrap is lightweight
          and runs against the host kernel; nspawn boots an ephemeral
          systemd-nspawn container off a sealed, versioned rootfs.
          <b> Mode change applies on the next SSH connect — no reload needed.</b>
        </Typography.Paragraph>

        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item
              label="Sandbox Mode"
              name="ssh_sandbox_mode"
              rules={[{ required: true }]}
              extra="Bubblewrap = no rootfs needed. nspawn = build an image first via 'jabali nspawn build'."
            >
              <Select
                options={[
                  { value: "bubblewrap", label: "Bubblewrap (default, lightweight)" },
                  { value: "nspawn", label: "systemd-nspawn (full container)" },
                ]}
                style={{ width: "100%" }}
              />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              label="Default nspawn Image"
              name="default_nspawn_image_version"
              extra={
                nspawnImages.length === 0
                  ? "No images built yet. Run 'jabali nspawn build --version v1 --snapshot ...' to seed."
                  : "Pinned to new SSH-enabled users at create. Existing users keep their pin."
              }
            >
              <Select
                showSearch
                allowClear
                placeholder="debian-12-v1"
                options={nspawnImages.map((img) => ({
                  value: img.name,
                  label: img.name,
                }))}
                style={{ width: "100%" }}
              />
            </Form.Item>
          </Col>
        </Row>
      </Card>

      <PanelSSLCard />

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

// StorageSettingsTab — File Manager upload cap + POSIX quota enforcement.
// Owns its own form so unsaved edits are independent of the General tab,
// and the partial PATCH only ships the two storage fields so a Save here
// can't clobber identity/SSH/timezone settings.
const StorageSettingsTab = () => {
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
        disk_quota_enabled: values.disk_quota_enabled || false,
        upload_max_size_mb: values.upload_max_size_mb || 1024,
      });
      notify({ type: "success", message: "Storage settings saved" });
      form.setFieldsValue(resp.data);
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
      <Card title="File Manager" style={{ marginBottom: 16 }}>
        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item
              label="Maximum Upload Size (MB)"
              name="upload_max_size_mb"
              rules={[
                { required: true, message: "Required" },
                {
                  type: "number",
                  min: 1,
                  max: 10240,
                  message: "Between 1 and 10240 MB",
                },
              ]}
              tooltip="Hard cap on a single file upload via the File Manager. Applies to both single-multipart and chunked paths. Defaults to 1024 MB (1 GB)."
            >
              <InputNumber
                min={1}
                max={10240}
                step={64}
                style={{ width: "100%" }}
                addonAfter="MB"
              />
            </Form.Item>
          </Col>
        </Row>
      </Card>

      <Card title="Disk Quotas" style={{ marginBottom: 16 }}>
        <Row gutter={16}>
          <Col xs={24}>
            <div style={{ marginBottom: 16 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
                <Form.Item name="disk_quota_enabled" valuePropName="checked" noStyle>
                  <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
                </Form.Item>
                <Typography.Text>POSIX Disk Quota Enforcement</Typography.Text>
              </div>
              <Typography.Text type="secondary">
                When enabled, the reconciler applies per-user disk-quota limits from packages and overrides.
                When disabled, disk-quota fields in Packages are read-only and only cgroup limits (cpu / memory / io / tasks) are enforced.
              </Typography.Text>
              <Alert
                style={{ marginTop: 12 }}
                type="info"
                showIcon
                message="Kernel POSIX quota must be active on the filesystem holding /home"
                description={
                  <>
                    install.sh wires this up automatically (works on dedicated <code>/home</code> partitions
                    and on <code>/</code>-shared <code>/home</code> via ext4 hidden quota inodes).
                    Only system UIDs ≥ 1000 ever get a setquota call, so root + system daemons stay
                    unlimited. Verify with <code>quotaon -p -a</code> before flipping this on; if no
                    quota is reported active, re-run install.sh or set it up manually.
                  </>
                }
              />
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
        >
          Save Storage Settings
        </Button>
      </Space>
    </Form>
  );
};

type SettingsTabKey = "general" | "storage" | "dns" | "email" | "branding";

const BrandingSettingsTab = () => (
  <>
    <BrandingCard />
    <PageTemplatesCard />
  </>
);

export const ServerSettingsPage = () => {
  const [activeTab, setActiveTab] = useState<SettingsTabKey>("general");

  return (
    <div style={{ maxWidth: 960 }}>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Server Settings
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Server identity, DNS nameserver names, branding, and page templates.
      </Typography.Paragraph>

      {/* Card.tabList renders the tab strip attached to the card body —
          each tab owns an independent form, so unsaved edits in the
          inactive tab are lost on switch (mirrors the Users page pattern). */}
      <Card
        tabList={[
          {
            key: "general",
            tab: (
              <span>
                <SettingOutlined style={{ marginRight: 8 }} />
                General
              </span>
            ),
          },
          {
            key: "storage",
            tab: (
              <span>
                <HddOutlined style={{ marginRight: 8 }} />
                Storage
              </span>
            ),
          },
          {
            key: "dns",
            tab: (
              <span>
                <GlobalOutlined style={{ marginRight: 8 }} />
                DNS
              </span>
            ),
          },
          {
            key: "email",
            tab: (
              <span>
                <MailOutlined style={{ marginRight: 8 }} />
                Email
              </span>
            ),
          },
          {
            key: "branding",
            tab: (
              <span>
                <BgColorsOutlined style={{ marginRight: 8 }} />
                Branding
              </span>
            ),
          },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as SettingsTabKey)}
      >
        {activeTab === "general" && <GeneralSettingsTab />}
        {activeTab === "storage" && <StorageSettingsTab />}
        {activeTab === "dns" && <DNSSettingsTab />}
        {activeTab === "email" && <EmailCard />}
        {activeTab === "branding" && <BrandingSettingsTab />}
      </Card>
    </div>
  );
};
