// Login page — a thin AntD form bound to refine's useLogin hook.
//
// Refine's @refinedev/antd ships an <AuthPage type="login" /> that does
// all of this out of the box, but writing it explicitly keeps the
// surface area obvious and easy to tweak (e.g. strip the "register"
// link, which we don't support).
import { useLogin } from "@refinedev/core";
import { Button, Card, Form, Input, Space, Typography } from "antd";

type LoginValues = { email: string; password: string };

export const LoginPage = () => {
  const { mutate: login, isPending } = useLogin<LoginValues>();

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "#f5f5f5",
      }}
    >
      <Card style={{ width: 380 }}>
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Typography.Title level={3} style={{ margin: 0 }}>
            Jabali Panel
          </Typography.Title>
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
