// AdminIPCreate — admin form for adding an IP to the managed pool.
//
// Two warning banners reflect non-jabali responsibilities (persistence
// and host firewall) per ADR-0048 / plans/m24-ip-manager.md F-H.
import { Alert, Button, Card, Form, Input, Switch, Typography, message } from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { useState } from "react";
import { useNavigate } from "react-router";

import { useCreateMutation } from "../../../hooks/useQueries";

type IPCreateInput = {
  address: string;
  label: string;
  is_user_selectable: boolean;
};

type IPCreated = {
  id: number;
  warnings?: string[];
};

// IPv4 + IPv6 regex pair. Lenient on purpose — server-side validation
// (validateRoutableIP) is the source of truth; the regex catches obvious
// typos client-side without forking RFC parsers in TS.
const IPV4 = /^(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)$/;
const IPV6 = /^[0-9a-fA-F:]+$/;
function isProbablyIP(addr: string): boolean {
  return IPV4.test(addr) || (addr.includes(":") && IPV6.test(addr));
}

export const AdminIPCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<IPCreateInput>();
  const [warnings, setWarnings] = useState<string[]>([]);

  const createMutation = useCreateMutation<IPCreated, IPCreateInput>({
    resource: "admin/ips",
  });

  const handleFinish = async (values: IPCreateInput) => {
    setWarnings([]);
    try {
      const result = await createMutation.mutateAsync(values);
      if (result.warnings && result.warnings.length > 0) {
        // Non-fatal — IP is bound; the admin just needs to investigate.
        setWarnings(result.warnings);
        message.warning("IP added with warnings — review below");
        return;
      }
      message.success("IP added to pool");
      navigate("/jabali-admin/ips");
    } catch (err: unknown) {
      message.error(err instanceof Error ? err.message : "Failed to add IP");
    }
  };

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Add IP address
      </Typography.Title>

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="Persistence is your responsibility"
        description={
          <span>
            jabali binds this IP ephemerally via <code>ip addr add</code>. For the binding
            to survive a reboot, add the address via your provider&apos;s network
            configuration (Hetzner robot, Vultr additional IP, netplan, or{" "}
            <code>/etc/network/interfaces.d/</code>).
          </span>
        }
      />

      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 24 }}
        message="Verify your firewall"
        description={
          <span>
            After adding, ensure your host firewall allows inbound TCP 80 and 443 to this
            address. Check with{" "}
            <code>iptables -L INPUT -v -n | grep &lt;ip&gt;</code> (or the equivalent for
            nft / ufw / firewalld).
          </span>
        }
      />

      <Form<IPCreateInput>
        form={form}
        layout="vertical"
        initialValues={{ label: "", is_user_selectable: false }}
        onFinish={handleFinish}
      >
        <Form.Item
          label="Address"
          name="address"
          rules={[
            { required: true, message: "IP address is required" },
            {
              validator: (_, v) =>
                v && !isProbablyIP(v.trim())
                  ? Promise.reject(new Error("Must be a valid IPv4 or IPv6 address"))
                  : Promise.resolve(),
            },
          ]}
        >
          <Input placeholder="203.0.113.50 or 2001:db8::1" />
        </Form.Item>

        <Form.Item
          label="Label"
          name="label"
          tooltip="Optional human-readable note (provider, purpose, etc.)"
        >
          <Input placeholder="e.g., 'extra-customer-set'" />
        </Form.Item>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 24 }}>
          <Form.Item name="is_user_selectable" valuePropName="checked" noStyle>
            <Switch
              checkedChildren={<CheckOutlined />}
              unCheckedChildren={<CloseOutlined />}
            />
          </Form.Item>
          <Typography.Text>User-selectable in domain picker</Typography.Text>
        </div>

        {warnings.length > 0 ? (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message="Post-bind probe warnings"
            description={
              <ul style={{ marginBottom: 0 }}>
                {warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            }
          />
        ) : null}

        <Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={createMutation.isPending}
          >
            Add IP
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
