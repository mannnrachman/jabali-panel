// PanelSSLCard — admin Server Settings panel for the panel-hostname's
// TLS cert (M32, ADR-0066). Embeds inside the General tab so the
// hostname/admin-email cards above it are visually adjacent — both
// inputs feed the routability gate this card checks.
import {
  CheckCircleOutlined,
  CloseOutlined,
  ReloadOutlined,
  SafetyOutlined,
} from "@icons";
import {
  Alert,
  Button,
  Card,
  Popconfirm,
  Space,
  Switch,
  Tag,
  Typography,
  notification,
} from "antd";
import {
  type PanelCertificate,
  usePanelCertificate,
  usePanelCertificateIssue,
  usePanelCertificateToggle,
} from "../../../hooks/usePanelCertificate";

function statusTag(c: PanelCertificate) {
  switch (c.status) {
    case "issued":
      return (
        <Tag color="success">
          Issued by Let&apos;s Encrypt{c.staging ? " (staging)" : ""}
        </Tag>
      );
    case "pending_acme":
      return <Tag color="processing">Issuing…</Tag>;
    case "pending_acme_retry":
      return <Tag color="warning">Pending retry</Tag>;
    case "failed":
      return <Tag color="error">Failed</Tag>;
    case "self_signed":
    default:
      return <Tag>Self-signed</Tag>;
  }
}

function expiryHint(c: PanelCertificate): string | null {
  if (c.status !== "issued" || !c.expires_at) return null;
  const ms = new Date(c.expires_at).getTime() - Date.now();
  if (Number.isNaN(ms)) return null;
  const days = Math.floor(ms / (24 * 3600 * 1000));
  if (days < 0) return "Expired";
  if (days < 7) return `Expires in ${days} day${days === 1 ? "" : "s"}`;
  return `Expires in ${days} days`;
}

export function PanelSSLCard() {
  const q = usePanelCertificate();
  const toggle = usePanelCertificateToggle();
  const issue = usePanelCertificateIssue();

  if (q.isPending) {
    return <Card title="Panel SSL" loading style={{ marginBottom: 16 }} />;
  }
  if (q.isError || !q.data) {
    return (
      <Card title="Panel SSL" style={{ marginBottom: 16 }}>
        <Alert
          type="error"
          message="Failed to load panel SSL state"
          description={String((q.error as Error)?.message ?? "")}
          showIcon
        />
      </Card>
    );
  }
  const c = q.data;
  const expiry = expiryHint(c);

  return (
    <Card
      title={
        <Space>
          <SafetyOutlined />
          <span>Panel SSL</span>
        </Space>
      }
      style={{ marginBottom: 16 }}
      extra={
        <Button
          icon={<ReloadOutlined />}
          size="small"
          onClick={() => q.refetch()}
        >
          Refresh
        </Button>
      }
    >
      <Space direction="vertical" size={12} style={{ width: "100%" }}>
        <Space wrap>
          {statusTag(c)}
          {c.routable ? (
            <Tag icon={<CheckCircleOutlined />} color="success">
              Public-routable
            </Tag>
          ) : (
            <Tag icon={<CloseOutlined />}>
              Not routable
              {c.routable_reason ? ` — ${c.routable_reason}` : ""}
            </Tag>
          )}
          {expiry && <Tag color={expiry === "Expired" ? "error" : undefined}>{expiry}</Tag>}
        </Space>

        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          Replace the panel&apos;s self-signed certificate with a Let&apos;s
          Encrypt cert covering <code>{c.hostname || "<hostname>"}</code> and{" "}
          <code>mail.{c.hostname || "<hostname>"}</code>. Self-signed remains
          the fallback if issuance fails.
        </Typography.Paragraph>

        <Space wrap>
          <Switch
            checked={c.use_le}
            disabled={!c.routable && !c.use_le}
            loading={toggle.isPending}
            onChange={(v) => {
              toggle.mutate(
                { use_le: v },
                {
                  onSuccess: () =>
                    notification.success({
                      message: v
                        ? "Let's Encrypt enabled — issuance will run on the next reconciler tick"
                        : "Let's Encrypt disabled — existing cert stays in place until expiry",
                    }),
                  onError: (e) =>
                    notification.error({
                      message: "Failed to update toggle",
                      description: String((e as Error).message),
                    }),
                },
              );
            }}
          />
          <Typography.Text>Use Let&apos;s Encrypt for this hostname</Typography.Text>
        </Space>

        <Space wrap>
          <Switch
            checked={c.staging}
            disabled={!c.use_le}
            loading={toggle.isPending}
            onChange={(v) => toggle.mutate({ staging: v })}
          />
          <Typography.Text>
            Use Let&apos;s Encrypt staging (testing only — browsers will warn
            about the test cert)
          </Typography.Text>
        </Space>

        {c.use_le && (c.status === "pending_acme_retry" || c.status === "failed") && (
          <Alert
            type="warning"
            showIcon
            message="Last attempt failed"
            description={
              <>
                <div>
                  {c.last_error || "No error captured."}
                </div>
                <div style={{ marginTop: 4 }}>
                  Attempt {c.attempt_count}. Reconciler will retry every 3 hours;
                  click Retry now to bypass the backoff.
                </div>
              </>
            }
            action={
              <Popconfirm
                title="Force a Let's Encrypt issuance now?"
                onConfirm={() =>
                  issue.mutate(undefined, {
                    onSuccess: () =>
                      notification.success({ message: "Issued" }),
                    onError: (e) =>
                      notification.error({
                        message: "Issue failed",
                        description: String((e as Error).message),
                      }),
                  })
                }
              >
                <Button size="small" loading={issue.isPending}>
                  Retry now
                </Button>
              </Popconfirm>
            }
          />
        )}
      </Space>
    </Card>
  );
}
