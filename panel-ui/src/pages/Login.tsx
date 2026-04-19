// Login page — two-stage state machine (password → 2FA challenge).
//
// Stage "password": normal email + password form. POSTs /auth/login directly
// (bypassing refine's useLogin) so we can branch on the 2FA response shape.
//   - Response { access_token, user } → happy path, set token + navigate
//   - Response { twofa_pending: true, twofa_pending_token } → transition
//     to "challenge" stage with the pending token held in local state
//
// Stage "challenge": 6-digit TOTP input by default, "Use a backup code
// instead" link swaps to the 8-digit variant. POSTs /auth/2fa/challenge.
// Pending token is kept in component state only — a page reload forces
// re-entering the password, which is fine for a 5-minute token.
//
// CLI token redemption (?cli_token=...) is unchanged from the previous
// implementation — that flow doesn't interact with 2FA because the
// break-glass admin CLI is the escape hatch by design.
import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Space,
  Typography,
  theme,
} from "antd";

import { apiClient, setAccessToken } from "../apiClient";
import { clearIdentity, getIdentity } from "../identity";

type LoginValues = { email: string; password: string };

type LoginResponse = {
  access_token?: string;
  user?: { id: string; email: string; is_admin: boolean };
  twofa_pending?: boolean;
  twofa_pending_token?: string;
};

type ChallengeValues = { code: string };

type Stage =
  | { kind: "password" }
  | { kind: "challenge"; pendingToken: string; useBackup: boolean };

const ADMIN_HOME = "/jabali-admin/dashboard";
const USER_HOME = "/jabali-panel";

export const LoginPage = () => {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [cliTokenError, setCliTokenError] = useState<string | null>(null);
  const [isRedeemingCLI, setIsRedeemingCLI] = useState(false);
  const { token } = theme.useToken();

  const [stage, setStage] = useState<Stage>({ kind: "password" });
  const [submitting, setSubmitting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Attempt to redeem CLI token on mount if present in query params
  useEffect(() => {
    const cliToken = searchParams.get("cli_token");
    if (!cliToken) return;

    setIsRedeemingCLI(true);
    apiClient
      .post<LoginResponse>("/auth/cli-login", { cli_token: cliToken })
      .then(async (response) => {
        if (!response.data.access_token || !response.data.user) {
          setCliTokenError("Invalid or expired login link");
          setIsRedeemingCLI(false);
          return;
        }
        // Mark tab as impersonation-mode BEFORE setAccessToken so the setter
        // mirrors the token to sessionStorage (survives reload; no cookie).
        sessionStorage.setItem("no_refresh", "1");
        setAccessToken(response.data.access_token);
        const home = response.data.user.is_admin ? ADMIN_HOME : USER_HOME;
        navigate(home, { replace: true });
      })
      .catch(() => {
        setCliTokenError("Invalid or expired login link");
        setIsRedeemingCLI(false);
      });
  }, [searchParams, navigate]);

  const finishLogin = async () => {
    // Access token is set; load identity and navigate by role.
    clearIdentity();
    const me = await getIdentity();
    navigate(me?.isAdmin ? ADMIN_HOME : USER_HOME, { replace: true });
  };

  const onPassword = async (values: LoginValues) => {
    setErrorMsg(null);
    setSubmitting(true);
    try {
      const resp = await apiClient.post<LoginResponse>("/auth/login", values);
      if (resp.data.twofa_pending && resp.data.twofa_pending_token) {
        setStage({
          kind: "challenge",
          pendingToken: resp.data.twofa_pending_token,
          useBackup: false,
        });
        return;
      }
      if (resp.data.access_token) {
        setAccessToken(resp.data.access_token);
        await finishLogin();
        return;
      }
      setErrorMsg("Unexpected server response. Please try again.");
    } catch (err) {
      const code =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "login_failed";
      setErrorMsg(
        code === "invalid_credentials"
          ? "Incorrect email or password."
          : code === "rate_limited"
            ? "Too many attempts — try again in a minute."
            : "Could not sign in. Please try again.",
      );
    } finally {
      setSubmitting(false);
    }
  };

  const onChallenge = async (values: ChallengeValues) => {
    if (stage.kind !== "challenge") return;
    setErrorMsg(null);
    setSubmitting(true);
    try {
      const body = stage.useBackup
        ? {
            twofa_pending_token: stage.pendingToken,
            backup_code: values.code,
          }
        : { twofa_pending_token: stage.pendingToken, code: values.code };
      const resp = await apiClient.post<LoginResponse>(
        "/auth/2fa/challenge",
        body,
      );
      if (resp.data.access_token) {
        setAccessToken(resp.data.access_token);
        await finishLogin();
        return;
      }
      setErrorMsg("Unexpected server response. Please try again.");
    } catch (err) {
      const code =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "invalid_2fa_code";
      setErrorMsg(
        code === "invalid_2fa_code"
          ? stage.useBackup
            ? "That backup code isn't right (or it's already been used)."
            : "That code isn't right. Make sure your device's time is correct."
          : code === "invalid_token"
            ? "Your 2FA session expired. Please sign in again."
            : code === "rate_limited"
              ? "Too many attempts — try again in a minute."
              : "Could not verify. Please try again.",
      );
      // If the pending token is no longer valid, bounce back to password step.
      if (code === "invalid_token") {
        setStage({ kind: "password" });
      }
    } finally {
      setSubmitting(false);
    }
  };

  // If redeeming CLI token, show loading state
  if (isRedeemingCLI) {
    return (
      <div
        style={{
          minHeight: "100vh",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: token.colorBgLayout,
        }}
      >
        <Card style={{ width: 380 }}>
          <Space direction="vertical" size="large" style={{ width: "100%" }}>
            <Typography.Title level={3} style={{ margin: 0 }}>
              Signing you in...
            </Typography.Title>
            <div
              style={{ textAlign: "center", color: token.colorTextSecondary }}
            >
              Processing your login link
            </div>
          </Space>
        </Card>
      </div>
    );
  }

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: token.colorBgLayout,
      }}
    >
      <Card style={{ width: 380 }}>
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Typography.Title level={3} style={{ margin: 0 }}>
            Jabali Panel
          </Typography.Title>
          {cliTokenError && (
            <Alert
              message={cliTokenError}
              type="error"
              showIcon
              closable
              onClose={() => setCliTokenError(null)}
            />
          )}
          {errorMsg && (
            <Alert
              message={errorMsg}
              type="error"
              showIcon
              closable
              onClose={() => setErrorMsg(null)}
            />
          )}
          {stage.kind === "password" ? (
            <Form<LoginValues>
              layout="vertical"
              requiredMark={false}
              onFinish={onPassword}
            >
              <Form.Item
                label="Email"
                name="email"
                rules={[
                  { required: true, message: "Enter your email" },
                  { type: "email", message: "Must be a valid email" },
                ]}
              >
                <Input autoComplete="email" autoFocus />
              </Form.Item>
              <Form.Item
                label="Password"
                name="password"
                rules={[{ required: true, message: "Enter your password" }]}
              >
                <Input.Password autoComplete="current-password" />
              </Form.Item>
              <Form.Item style={{ marginBottom: 0 }}>
                <Button
                  type="primary"
                  htmlType="submit"
                  block
                  loading={submitting}
                >
                  Sign in
                </Button>
              </Form.Item>
            </Form>
          ) : (
            <ChallengeForm
              stage={stage}
              submitting={submitting}
              onSubmit={onChallenge}
              onToggleBackup={() =>
                setStage((prev) =>
                  prev.kind === "challenge"
                    ? { ...prev, useBackup: !prev.useBackup }
                    : prev,
                )
              }
              onBack={() => setStage({ kind: "password" })}
            />
          )}
        </Space>
      </Card>
    </div>
  );
};

function ChallengeForm({
  stage,
  submitting,
  onSubmit,
  onToggleBackup,
  onBack,
}: {
  stage: { kind: "challenge"; pendingToken: string; useBackup: boolean };
  submitting: boolean;
  onSubmit: (v: ChallengeValues) => void;
  onToggleBackup: () => void;
  onBack: () => void;
}) {
  return (
    <Form<ChallengeValues>
      layout="vertical"
      requiredMark={false}
      onFinish={onSubmit}
      // Changing useBackup remounts the input via `key` so the value clears
      // and the maxLength/pattern validator applies to the right format.
      key={stage.useBackup ? "backup" : "totp"}
    >
      <Typography.Paragraph style={{ marginBottom: 0 }}>
        {stage.useBackup
          ? "Enter one of your backup codes."
          : "Enter the 6-digit code from your authenticator app."}
      </Typography.Paragraph>
      <Form.Item
        label={stage.useBackup ? "Backup code" : "6-digit code"}
        name="code"
        rules={
          stage.useBackup
            ? [
                { required: true, message: "Required" },
                { len: 8, message: "Must be 8 digits" },
                { pattern: /^\d{8}$/, message: "Digits only" },
              ]
            : [
                { required: true, message: "Required" },
                { len: 6, message: "Must be 6 digits" },
                { pattern: /^\d{6}$/, message: "Digits only" },
              ]
        }
      >
        <Input
          inputMode="numeric"
          maxLength={stage.useBackup ? 8 : 6}
          autoComplete="one-time-code"
          autoFocus
        />
      </Form.Item>
      <Form.Item style={{ marginBottom: 8 }}>
        <Button type="primary" htmlType="submit" block loading={submitting}>
          Verify
        </Button>
      </Form.Item>
      <Space style={{ width: "100%", justifyContent: "space-between" }}>
        <Button type="link" size="small" onClick={onToggleBackup}>
          {stage.useBackup ? "Use authenticator instead" : "Use backup code"}
        </Button>
        <Button type="link" size="small" onClick={onBack}>
          Start over
        </Button>
      </Space>
    </Form>
  );
}
