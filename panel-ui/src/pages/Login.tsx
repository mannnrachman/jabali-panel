// Login page — a thin AntD form bound to refine's useLogin hook.
//
// Refine's @refinedev/antd ships an <AuthPage type="login" /> that does
// all of this out of the box, but writing it explicitly keeps the
// surface area obvious and easy to tweak (e.g. strip the "register"
// link, which we don't support).
//
// CLI token redemption:
// If a cli_token is present in the URL query params (e.g. from "jabali-panel admin login"),
// the page automatically redeems it via POST /api/v1/auth/cli-login and navigates to the dashboard.
import { useEffect, useState } from "react";
import { useLogin } from "@refinedev/core";
import { useNavigate, useSearchParams } from "react-router";
import { Button, Card, Form, Input, Space, Typography, Alert, theme } from "antd";
import { apiClient, setAccessToken } from "../apiClient";

type LoginValues = { email: string; password: string };

type LoginResponse = {
  access_token: string;
  user: {
    id: string;
    email: string;
    is_admin: boolean;
  };
};

export const LoginPage = () => {
  const { mutate: login, isPending } = useLogin<LoginValues>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [cliTokenError, setCliTokenError] = useState<string | null>(null);
  const [isRedeemingCLI, setIsRedeemingCLI] = useState(false);
  const { token } = theme.useToken();

  // Attempt to redeem CLI token on mount if present in query params
  useEffect(() => {
    const cliToken = searchParams.get("cli_token");
    if (!cliToken) return;

    setIsRedeemingCLI(true);
    apiClient
      .post<LoginResponse>("/auth/cli-login", { cli_token: cliToken })
      .then((response) => {
        // Store access token and set no_refresh flag for impersonation sessions
        // This prevents the 401 interceptor from attempting to refresh the token
        sessionStorage.setItem("no_refresh", "1");
        setAccessToken(response.data.access_token);
        navigate("/jabali-admin/dashboard", { replace: true });
      })
      .catch(() => {
        // Generic error message, don't leak details
        setCliTokenError("Invalid or expired login link");
        setIsRedeemingCLI(false);
      });
  }, [searchParams, navigate]);

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
            <div style={{ textAlign: "center", color: token.colorTextSecondary }}>
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
          <Form<LoginValues>
            layout="vertical"
            requiredMark={false}
            onFinish={(values) => login(values)}
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
                loading={isPending}
              >
                Sign in
              </Button>
            </Form.Item>
          </Form>
        </Space>
      </Card>
    </div>
  );
};
