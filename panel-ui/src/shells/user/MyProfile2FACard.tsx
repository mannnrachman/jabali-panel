// 2FA (TOTP) management card for MyProfile.
//
// Three states, all rendered by this one component:
//   disabled  → "Enable 2FA" button → enrolment modal (QR → verify → backup codes)
//   enabled   → status badge + "Regenerate backup codes" + "Disable 2FA"
//   loading   → spinner while we read totp_enabled from GET /users/:id
//
// Backup codes are shown EXACTLY ONCE (on verify success and on regen). The
// confirmation checkbox is the only way to close the modal — prevents the
// user from clicking through without saving them anywhere.
import { useEffect, useState } from "react";
import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Form,
  Input,
  List,
  Modal,
  QRCode,
  Space,
  Spin,
  Typography,
  message,
} from "antd";

import { apiClient } from "../../apiClient";

type EnrollResponse = { secret: string; otpauth_url: string };
type VerifyResponse = { backup_codes: string[] };
type RegenResponse = { backup_codes: string[] };

type Props = {
  userId: string;
};

export function MyProfile2FACard({ userId }: Props) {
  const [loading, setLoading] = useState(true);
  const [enabled, setEnabled] = useState(false);
  const [enrolOpen, setEnrolOpen] = useState(false);
  const [regenOpen, setRegenOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);

  const refresh = async () => {
    setLoading(true);
    try {
      const resp = await apiClient.get<{ totp_enabled: boolean }>(
        `/users/${userId}`,
      );
      setEnabled(resp.data.totp_enabled);
    } catch {
      // leave whatever stale state we had; parent page will show an error
      // on its next interaction
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, [userId]);

  const onEnrolled = async () => {
    setEnrolOpen(false);
    await refresh();
  };

  const onDisabled = async () => {
    setDisableOpen(false);
    await refresh();
  };

  return (
    <Card title="Two-factor authentication">
      {loading ? (
        <Spin />
      ) : enabled ? (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Badge status="success" text="Enabled" />
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            You'll be asked for a code from your authenticator app each time you
            sign in.
          </Typography.Paragraph>
          <Space>
            <Button onClick={() => setRegenOpen(true)}>
              Regenerate backup codes
            </Button>
            <Button danger onClick={() => setDisableOpen(true)}>
              Disable 2FA
            </Button>
          </Space>
        </Space>
      ) : (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Badge status="default" text="Not enabled" />
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            Protect your account with a second factor. Compatible with Google
            Authenticator, 1Password, Authy, Bitwarden.
          </Typography.Paragraph>
          <Button type="primary" onClick={() => setEnrolOpen(true)}>
            Enable 2FA
          </Button>
        </Space>
      )}

      {enrolOpen && (
        <EnrolModal onClose={() => setEnrolOpen(false)} onSuccess={onEnrolled} />
      )}
      {regenOpen && (
        <RegenBackupModal
          onClose={() => setRegenOpen(false)}
          onSuccess={() => setRegenOpen(false)}
        />
      )}
      {disableOpen && (
        <DisableModal onClose={() => setDisableOpen(false)} onSuccess={onDisabled} />
      )}
    </Card>
  );
}

// ---------- enrolment modal ----------

function EnrolModal({
  onClose,
  onSuccess,
}: {
  onClose: () => void;
  onSuccess: () => void;
}) {
  // Stage machine: start → scan (QR+secret) → codes (10 backup codes).
  const [stage, setStage] = useState<"scan" | "codes">("scan");
  const [enrolment, setEnrolment] = useState<EnrollResponse | null>(null);
  const [codes, setCodes] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [verifying, setVerifying] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [form] = Form.useForm<{ code: string }>();

  useEffect(() => {
    apiClient
      .post<EnrollResponse>("/auth/2fa/enroll")
      .then((resp) => setEnrolment(resp.data))
      .catch((err) => {
        const detail =
          (err as { response?: { data?: { error?: string } } }).response?.data
            ?.error ?? "unknown";
        if (detail === "already_enabled") {
          message.info("2FA is already enabled on this account.");
          onClose();
          return;
        }
        message.error("Could not start 2FA enrolment. Please try again.");
        onClose();
      })
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onVerify = async (values: { code: string }) => {
    setVerifying(true);
    try {
      const resp = await apiClient.post<VerifyResponse>("/auth/2fa/verify", {
        code: values.code,
      });
      setCodes(resp.data.backup_codes);
      setStage("codes");
    } catch (err) {
      const detail =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "unknown";
      message.error(
        detail === "invalid_code"
          ? "That code isn't right. Make sure the time on your device is correct and try the next code."
          : "Could not verify. Please try again.",
      );
    } finally {
      setVerifying(false);
    }
  };

  return (
    <Modal
      open
      title={stage === "scan" ? "Enable 2FA" : "Save your backup codes"}
      onCancel={onClose}
      footer={null}
      maskClosable={false}
      width={520}
    >
      {stage === "scan" ? (
        loading || !enrolment ? (
          <div style={{ textAlign: "center", padding: 40 }}>
            <Spin />
          </div>
        ) : (
          <Space direction="vertical" size="middle" style={{ width: "100%" }}>
            <Typography.Paragraph style={{ marginBottom: 0 }}>
              Scan this QR code with your authenticator app, then enter the
              6-digit code it shows.
            </Typography.Paragraph>
            <div style={{ textAlign: "center" }}>
              <QRCode value={enrolment.otpauth_url} size={200} />
            </div>
            <Typography.Paragraph
              type="secondary"
              style={{ marginBottom: 0, fontSize: 12 }}
            >
              Can't scan? Enter this key manually:{" "}
              <Typography.Text code copyable>
                {enrolment.secret}
              </Typography.Text>
            </Typography.Paragraph>
            <Form<{ code: string }>
              form={form}
              layout="vertical"
              onFinish={onVerify}
              autoComplete="off"
            >
              <Form.Item
                label="6-digit code"
                name="code"
                rules={[
                  { required: true, message: "Required" },
                  { len: 6, message: "Must be 6 digits" },
                  { pattern: /^\d{6}$/, message: "Digits only" },
                ]}
              >
                <Input
                  inputMode="numeric"
                  maxLength={6}
                  autoComplete="one-time-code"
                  autoFocus
                />
              </Form.Item>
              <Form.Item style={{ marginBottom: 0 }}>
                <Space>
                  <Button onClick={onClose}>Cancel</Button>
                  <Button type="primary" htmlType="submit" loading={verifying}>
                    Verify & enable
                  </Button>
                </Space>
              </Form.Item>
            </Form>
          </Space>
        )
      ) : (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="warning"
            showIcon
            message="Save these backup codes now"
            description="Each code works once. Use one if you ever lose your authenticator app. You will NOT see them again."
          />
          <List
            size="small"
            bordered
            dataSource={codes}
            renderItem={(c) => (
              <List.Item>
                <Typography.Text code copyable>
                  {c}
                </Typography.Text>
              </List.Item>
            )}
          />
          <Button
            block
            onClick={() => {
              const blob = new Blob([codes.join("\n") + "\n"], {
                type: "text/plain",
              });
              const url = URL.createObjectURL(blob);
              const a = document.createElement("a");
              a.href = url;
              a.download = "jabali-backup-codes.txt";
              a.click();
              URL.revokeObjectURL(url);
            }}
          >
            Download as .txt
          </Button>
          <Checkbox
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
          >
            I've saved my backup codes somewhere safe
          </Checkbox>
          <Button
            type="primary"
            block
            disabled={!confirmed}
            onClick={onSuccess}
          >
            Done
          </Button>
        </Space>
      )}
    </Modal>
  );
}

// ---------- regenerate backup codes modal ----------

function RegenBackupModal({
  onClose,
  onSuccess,
}: {
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [codes, setCodes] = useState<string[] | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [form] = Form.useForm<{ code: string }>();

  const onRegen = async (values: { code: string }) => {
    setSubmitting(true);
    try {
      const resp = await apiClient.post<RegenResponse>(
        "/auth/2fa/regen-backup",
        { code: values.code },
      );
      setCodes(resp.data.backup_codes);
    } catch (err) {
      const detail =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "unknown";
      message.error(
        detail === "invalid_code"
          ? "That code isn't right."
          : "Could not regenerate codes. Please try again.",
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open
      title={codes ? "New backup codes" : "Regenerate backup codes"}
      onCancel={onClose}
      footer={null}
      maskClosable={false}
      width={480}
    >
      {codes === null ? (
        <Form<{ code: string }>
          form={form}
          layout="vertical"
          onFinish={onRegen}
          autoComplete="off"
        >
          <Typography.Paragraph>
            Enter a current 6-digit code from your authenticator app. Your old
            backup codes will be invalidated.
          </Typography.Paragraph>
          <Form.Item
            label="6-digit code"
            name="code"
            rules={[
              { required: true, message: "Required" },
              { len: 6, message: "Must be 6 digits" },
              { pattern: /^\d{6}$/, message: "Digits only" },
            ]}
          >
            <Input
              inputMode="numeric"
              maxLength={6}
              autoComplete="one-time-code"
              autoFocus
            />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0 }}>
            <Space>
              <Button onClick={onClose}>Cancel</Button>
              <Button type="primary" htmlType="submit" loading={submitting}>
                Regenerate
              </Button>
            </Space>
          </Form.Item>
        </Form>
      ) : (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="warning"
            showIcon
            message="Save these backup codes now"
            description="Each code works once. You will NOT see them again."
          />
          <List
            size="small"
            bordered
            dataSource={codes}
            renderItem={(c) => (
              <List.Item>
                <Typography.Text code copyable>
                  {c}
                </Typography.Text>
              </List.Item>
            )}
          />
          <Checkbox
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
          >
            I've saved my backup codes somewhere safe
          </Checkbox>
          <Button
            type="primary"
            block
            disabled={!confirmed}
            onClick={onSuccess}
          >
            Done
          </Button>
        </Space>
      )}
    </Modal>
  );
}

// ---------- disable modal ----------

function DisableModal({
  onClose,
  onSuccess,
}: {
  onClose: () => void;
  onSuccess: () => void;
}) {
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<{ password: string; code: string }>();

  const onDisable = async (values: { password: string; code: string }) => {
    setSubmitting(true);
    try {
      await apiClient.post("/auth/2fa/disable", values);
      message.success("2FA disabled.");
      onSuccess();
    } catch (err) {
      const detail =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "unknown";
      message.error(
        detail === "invalid_credentials"
          ? "Password doesn't match."
          : detail === "invalid_code"
            ? "That code isn't right."
            : "Could not disable 2FA. Please try again.",
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open
      title="Disable 2FA"
      onCancel={onClose}
      footer={null}
      maskClosable={false}
      width={480}
    >
      <Form<{ password: string; code: string }>
        form={form}
        layout="vertical"
        onFinish={onDisable}
        autoComplete="off"
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="Your account will lose the second layer of protection."
        />
        <Form.Item
          label="Current password"
          name="password"
          rules={[{ required: true, message: "Required" }]}
        >
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          label="6-digit code from your authenticator"
          name="code"
          rules={[
            { required: true, message: "Required" },
            { len: 6, message: "Must be 6 digits" },
            { pattern: /^\d{6}$/, message: "Digits only" },
          ]}
        >
          <Input inputMode="numeric" maxLength={6} autoComplete="one-time-code" />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Space>
            <Button onClick={onClose}>Cancel</Button>
            <Button danger type="primary" htmlType="submit" loading={submitting}>
              Disable 2FA
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
}
