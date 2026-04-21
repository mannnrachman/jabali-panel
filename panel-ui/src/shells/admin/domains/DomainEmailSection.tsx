// DomainEmailSection — enable/disable email on a domain + show the
// DNS records the operator needs to publish.
//
// Mirrors DomainSSLSection's visual shape (Switch + small warning
// alerts) so the two feel native on DomainEdit. The DKIM key is
// surfaced as a copyable monospace block; MX/SPF/DMARC rows come
// from the API's `records` hint list. Live record-presence status
// is blueprint Step 5 scope (DNS autoconfig) — until then, hints
// render as static instructions (empty `status` column).
import { useState } from "react";
import {
  Alert,
  Button,
  Card,
  Skeleton,
  Space,
  Switch,
  Table,
  Typography,
  message,
} from "antd";
import { CopyOutlined } from "@ant-design/icons";

import {
  useDisableDomainEmail,
  useDomainEmail,
  useEnableDomainEmail,
  type DomainEmailDNSHint,
} from "../../../hooks/useMailboxes";

type Props = {
  domainId: string;
};

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    message.success("Copied to clipboard");
  } catch {
    message.error("Copy failed — select the field and copy manually");
  }
}

export const DomainEmailSection = ({ domainId }: Props) => {
  const { data, isLoading } = useDomainEmail(domainId);
  const enableMutation = useEnableDomainEmail();
  const disableMutation = useDisableDomainEmail();
  const [flipping, setFlipping] = useState(false);

  const onFlip = async (next: boolean) => {
    setFlipping(true);
    try {
      if (next) {
        await enableMutation.mutateAsync({ domainId });
        message.success("Email enabled — publish the DNS records below");
      } else {
        await disableMutation.mutateAsync({ domainId });
        message.success("Email disabled");
      }
    } catch (err: unknown) {
      const resp = (err as { response?: { data?: { error?: string; detail?: string } } })
        ?.response?.data;
      message.error(resp?.detail ?? resp?.error ?? "Failed to toggle email");
    } finally {
      setFlipping(false);
    }
  };

  if (isLoading && !data) {
    return <Skeleton active paragraph={{ rows: 3 }} />;
  }
  if (!data) {
    return <Alert type="error" showIcon title="Failed to load email state" />;
  }

  const enabled = data.email_enabled;
  const dkim = data.dkim_public_key ?? "";

  return (
    <Space orientation="vertical" style={{ width: "100%" }} size="large">
      <Space size="middle" align="center" wrap>
        <Switch checked={enabled} loading={flipping} onChange={onFlip} />
        <span>
          Incoming + outgoing mail for{" "}
          <Typography.Text code>{data.domain_name}</Typography.Text>
        </span>
      </Space>

      {enabled && !dkim && (
        // Paranoid guard: the panel is in an inconsistent state (enabled
        // but no DKIM material). Surface so the operator can disable +
        // re-enable rather than silently ship broken outbound mail.
        <Alert
          type="error"
          showIcon
          title="DKIM key missing"
          description="Email is enabled but no DKIM public key is stored. Toggle off and back on to regenerate."
        />
      )}

      {enabled && (
        <Card size="small" title="Publish these DNS records">
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 12 }}
            message="Live record-status checking ships in a later milestone."
            description="For now, publish each row in your domain's zone (the panel's DNS editor or your registrar). The DKIM record is the only one that's unique to this install — MX/SPF/DMARC are boilerplate."
          />
          <Table<DomainEmailDNSHint>
            size="small"
            pagination={false}
            dataSource={data.records}
            rowKey={(r) => `${r.type}:${r.name}`}
            columns={[
              {
                title: "Purpose",
                dataIndex: "purpose",
                width: 280,
                render: (text: string) => (
                  <Typography.Text type="secondary">{text}</Typography.Text>
                ),
              },
              { title: "Name", dataIndex: "name", width: 260 },
              { title: "Type", dataIndex: "type", width: 60 },
              {
                title: "Value",
                dataIndex: "value",
                render: (value: string) =>
                  value ? (
                    <Space size="small">
                      <Typography.Text
                        code
                        copyable={false}
                        style={{
                          wordBreak: "break-all",
                          fontSize: 12,
                        }}
                      >
                        {value}
                      </Typography.Text>
                      <Button
                        size="small"
                        icon={<CopyOutlined />}
                        onClick={() => copyText(value)}
                        aria-label="Copy value"
                      />
                    </Space>
                  ) : (
                    <Typography.Text type="secondary" italic>
                      generated on enable
                    </Typography.Text>
                  ),
              },
            ]}
          />
        </Card>
      )}
    </Space>
  );
};
