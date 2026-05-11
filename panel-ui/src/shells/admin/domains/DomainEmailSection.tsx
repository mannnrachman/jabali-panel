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
  Tag,
  Typography,
  message,
} from "antd";
import { CopyOutlined } from "@icons";

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
    <Space direction="vertical" style={{ width: "100%" }} size="large">
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

      {enabled && data.warnings && data.warnings.length > 0 && (
        // Panel-API sends these when an M6 record can't be written
        // because a user-edited row blocks the slot. One line per
        // blocked record — no header, no dismissal: the operator
        // needs to resolve them by touching DNS.
        <Alert
          type="warning"
          showIcon
          message="DNS autoconfig partially applied"
          description={
            <ul style={{ margin: 0, paddingInlineStart: 20 }}>
              {data.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          }
        />
      )}

      {enabled && (
        <Card size="small" title="DNS records">
          <Table<DomainEmailDNSHint>
            size="small"
            pagination={false}
            dataSource={data.records}
            scroll={{ x: "max-content" }}
            rowKey={(r) => `${r.type}:${r.name}`}
            columns={[
              {
                title: "Status",
                dataIndex: "status",
                width: 100,
                render: (status: string | undefined) => renderStatusTag(status),
              },
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

// renderStatusTag maps the panel-API's 4 documented status values to
// AntD tag colours. Empty string is "no live data" — the panel has no
// zone on file, usually because PowerDNS isn't wired in this install.
// "ok" is green (present + managed or matching), "missing" is default
// (zone has no row there), "conflict" is red (user-edited row blocks M6).
function renderStatusTag(status: string | undefined) {
  switch (status) {
    case "ok":
      return <Tag color="green">ok</Tag>;
    case "missing":
      return <Tag color="default">missing</Tag>;
    case "conflict":
      return <Tag color="red">conflict</Tag>;
    default:
      return <Tag color="default">—</Tag>;
  }
}
