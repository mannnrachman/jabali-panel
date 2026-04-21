import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Skeleton, Space, Switch, Tag, Tooltip, message } from "antd";
import { CheckOutlined, CloseOutlined, ReloadOutlined } from "@ant-design/icons";
import { Link } from "react-router";

import { apiClient } from "../../../apiClient";

type SSLStatus =
  | "pending"
  | "issuing"
  | "issued"
  | "failed"
  | "revoked"
  | "renewing"
  | "self_signed"
  | "pending_acme_retry";

type SSLCertificate = {
  status: SSLStatus;
  issued_at?: string;
  expires_at?: string;
  renewal_count: number;
  last_renewed_at?: string;
  last_error?: string;
  staging: boolean;
  cert_path?: string;
  key_path?: string;
  next_retry_at?: string;
  retry_count: number;
};

type ServerSettings = {
  admin_email?: string;
};

const STATUS_COLORS: Record<SSLStatus, string> = {
  pending: "default",
  issuing: "processing",
  renewing: "processing",
  issued: "green",
  failed: "red",
  revoked: "default",
  self_signed: "orange",
  pending_acme_retry: "gold",
};

function daysUntil(iso?: string): number | null {
  if (!iso) return null;
  const diff = new Date(iso).getTime() - Date.now();
  return Math.round(diff / 86_400_000);
}

type Props = {
  domainId: string;
  sslEnabled: boolean;
  onToggled: () => void;
};

export const DomainSSLSection = ({ domainId, sslEnabled, onToggled }: Props) => {
  const [cert, setCert] = useState<SSLCertificate | null>(null);
  const [certMissing, setCertMissing] = useState(false);
  const [loading, setLoading] = useState(true);
  const [toggling, setToggling] = useState(false);
  const [renewing, setRenewing] = useState(false);
  const [adminEmail, setAdminEmail] = useState<string | undefined>(undefined);

  const fetchCert = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiClient.get(`/domains/${domainId}/ssl`);
      setCert(res.data.ssl);
      setCertMissing(false);
    } catch (err: unknown) {
      const status = (err as { response?: { status?: number } })?.response?.status;
      if (status === 404) {
        setCert(null);
        setCertMissing(true);
      } else {
        message.error("Failed to load SSL status");
      }
    } finally {
      setLoading(false);
    }
  }, [domainId]);

  useEffect(() => {
    fetchCert();
    apiClient
      .get<{ settings: ServerSettings }>("/system/settings")
      .then((res) => setAdminEmail(res.data?.settings?.admin_email))
      .catch(() => undefined);
  }, [fetchCert]);

  const onFlip = async (next: boolean) => {
    setToggling(true);
    try {
      if (next) {
        await apiClient.post(`/domains/${domainId}/ssl`);
        message.success("SSL issuance scheduled");
      } else {
        await apiClient.delete(`/domains/${domainId}/ssl`);
        message.success("SSL revocation scheduled");
      }
      onToggled();
      await fetchCert();
    } catch (err: unknown) {
      const resp = (err as { response?: { data?: { error?: string; detail?: string } } })?.response?.data;
      if (resp?.error === "missing_admin_email") {
        message.error(
          resp.detail ?? "Set an admin email in Server Settings before enabling SSL.",
        );
      } else {
        message.error("Failed to toggle SSL");
      }
    } finally {
      setToggling(false);
    }
  };

  const onRenew = async () => {
    setRenewing(true);
    try {
      await apiClient.post(`/domains/${domainId}/ssl/renew`);
      message.success("Renewal scheduled");
      await fetchCert();
    } catch {
      message.error("Failed to schedule renewal");
    } finally {
      setRenewing(false);
    }
  };

  const onRetry = async () => {
    setRenewing(true);
    try {
      await apiClient.post(`/domains/${domainId}/ssl/retry`);
      message.success("Retry queued");
      await fetchCert();
    } catch {
      message.error("Failed to queue retry");
    } finally {
      setRenewing(false);
    }
  };

  if (loading) return <Skeleton active paragraph={{ rows: 2 }} />;

  const status = cert?.status;
  const days = daysUntil(cert?.expires_at);

  return (
    <Space orientation="vertical" style={{ width: "100%" }}>
      {!adminEmail && (
        <Alert
          type="warning"
          showIcon
          title="Set an admin email to use SSL"
          description={
            <>
              Let&apos;s Encrypt requires an email on the account. Configure it in{" "}
              <Link to="/jabali-admin/settings">Server Settings</Link>.
            </>
          }
        />
      )}

      {status === "self_signed" || status === "pending_acme_retry" ? (
        <Alert
          type="warning"
          showIcon
          title="Using self-signed certificate"
          description="This domain is using a self-signed certificate while Let's Encrypt issuance retries. Browsers will show a security warning until the real cert is issued."
        />
      ) : null}

      {status === "failed" ? (
        <Alert
          type="error"
          showIcon
          title="ACME issuance failed"
          description={
            <>
              {cert?.last_error && <div>Error: {cert.last_error}</div>}
              <Button
                type="primary"
                loading={renewing}
                onClick={onRetry}
                style={{ marginTop: 8 }}
              >
                Retry
              </Button>
            </>
          }
        />
      ) : null}

      <Space size="middle" align="center">
        <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />}
          checked={sslEnabled}
          loading={toggling}
          onChange={onFlip}
          disabled={!adminEmail && !sslEnabled}
        />
        <span>HTTPS / Let&apos;s Encrypt</span>

        {certMissing && sslEnabled && (
          <Tag color="default">pending issuance</Tag>
        )}
        {status && (
          <Tooltip title={status === "failed" ? cert?.last_error : undefined}>
            <Tag color={STATUS_COLORS[status]}>
              {status.replace(/_/g, " ")}
            </Tag>
          </Tooltip>
        )}
        {status === "issued" && days !== null && (
          <Tag color={days < 15 ? "orange" : "default"}>
            expires in {days} day{days === 1 ? "" : "s"}
          </Tag>
        )}
        {cert?.staging && <Tag color="purple">staging</Tag>}

        {status === "issued" && (
          <Button
            icon={<ReloadOutlined />}
            loading={renewing}
            onClick={onRenew}
          >
            Renew now
          </Button>
        )}
      </Space>
    </Space>
  );
};
