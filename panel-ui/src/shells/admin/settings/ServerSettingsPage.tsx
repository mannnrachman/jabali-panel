import { useEffect, useState } from "react";
import {
  SaveOutlined,
  WarningOutlined,
  CloudServerOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  Row,
  Space,
  Typography,
} from "antd";
import { useNotification } from "@refinedev/core";

import { apiClient } from "../../../apiClient";

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
  updated_at: string;
};

export const ServerSettingsPage = () => {
  const [form] = Form.useForm<ServerSettings>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [originalHostname, setOriginalHostname] = useState("");
  const { open: notify } = useNotification();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await apiClient.get<ServerSettings>("/admin/settings");
        if (cancelled) return;
        form.setFieldsValue(resp.data);
        setOriginalHostname(resp.data.hostname);
      } catch (err) {
        const e = err as { response?: { data?: { detail?: string } }; message?: string };
        notify?.({
          type: "error",
          message: "Failed to load settings",
          description: e.response?.data?.detail ?? e.message ?? "Unknown error",
        });
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
        ns1_name: values.ns1_name,
        ns1_ipv4: values.ns1_ipv4,
        ns2_name: values.ns2_name || "",
        ns2_ipv4: values.ns2_ipv4 || "",
        admin_email: values.admin_email || "",
      });
      notify?.({ type: "success", message: "Settings saved" });
      form.setFieldsValue(resp.data);
      setOriginalHostname(resp.data.hostname);
    } catch (err) {
      const e = err as { response?: { data?: { detail?: string } }; message?: string };
      notify?.({
        type: "error",
        message: "Failed to save",
        description: e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 960 }}>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        <CloudServerOutlined style={{ marginRight: 8 }} />
        Server Settings
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Server identity, DNS nameserver names, and administrative contact info.
      </Typography.Paragraph>

      <Form
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        disabled={loading}
      >
        {/* Warning when hostname is about to change */}
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
                message="Hostname change"
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

        <Card title="Identity" size="small" style={{ marginBottom: 16 }}>
          <Row gutter={16}>
            <Col span={12}>
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
            <Col span={12}>
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
            <Col span={12}>
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
            <Col span={12}>
              <Form.Item
                label="Public IPv6 (optional)"
                name="public_ipv6"
                rules={[
                  {
                    // Loose — server validates properly.
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

        <Card title="DNS Nameservers" size="small" style={{ marginBottom: 16 }}>
          <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
            These are the names and addresses your customers will set at their
            registrar. You typically run ns1 on this server and ns2 on a
            separate box. ns2 is optional at first — fill it in once you have
            a second nameserver provisioned.
          </Typography.Paragraph>

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item label="ns1 hostname" name="ns1_name">
                <Input placeholder="ns1.panel.example.com" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item label="ns1 IPv4" name="ns1_ipv4">
                <Input placeholder="203.0.113.5" />
              </Form.Item>
            </Col>
          </Row>

          <Divider orientation="left" plain style={{ fontSize: 12 }}>
            Secondary (optional)
          </Divider>

          <Row gutter={16}>
            <Col span={12}>
              <Form.Item label="ns2 hostname" name="ns2_name">
                <Input placeholder="ns2.panel.example.com" />
              </Form.Item>
            </Col>
            <Col span={12}>
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
            Save Settings
          </Button>
        </Space>
      </Form>
    </div>
  );
};
