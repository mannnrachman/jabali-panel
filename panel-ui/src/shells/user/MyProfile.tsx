// MyProfile — first user-panel page. Intentionally plain so the shell
// architecture gets exercised end-to-end before we build out real user
// features (domains, email, DNS, SSL, …).
//
// Shows the identity card + a change-password form. Password change
// hits PATCH /api/v1/users/:id with current_password — the server
// refuses the update if it doesn't match the stored hash.
import { Button, Card, Descriptions, Form, Input, Space, Typography, message } from "antd";
import { useEffect, useState } from "react";

import { apiClient } from "../../apiClient";
import { getIdentity, type Identity } from "../../identity";
import { MyProfile2FACard } from "./MyProfile2FACard";

type ChangePasswordForm = {
  current_password: string;
  password: string;
};

export function MyProfile() {
  const [me, setMe] = useState<Identity | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<ChangePasswordForm>();

  useEffect(() => {
    getIdentity().then(setMe);
  }, []);

  const onChangePassword = async (values: ChangePasswordForm) => {
    if (!me) return;
    setSubmitting(true);
    try {
      await apiClient.patch(`/users/${me.id}`, values);
      message.success("Password updated.");
      form.resetFields();
    } catch (err) {
      const detail =
        (err as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "unknown";
      message.error(
        detail === "invalid_credentials"
          ? "Current password is incorrect."
          : `Could not update password (${detail}).`,
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div style={{ padding: 24, maxWidth: 720, margin: "0 auto" }}>
      <Space direction="vertical" size="large" style={{ width: "100%" }}>
        <Typography.Title level={2} style={{ margin: 0 }}>
          My profile
        </Typography.Title>

        <Card title="Account" loading={!me}>
          {me && (
            <Descriptions column={1} size="small">
              <Descriptions.Item label="Email">{me.email}</Descriptions.Item>
              <Descriptions.Item label="User ID">
                <Typography.Text code>{me.id}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>
          )}
        </Card>

        {me && <MyProfile2FACard userId={me.id} />}

        <Card title="Change password">
          <Form<ChangePasswordForm>
            form={form}
            layout="vertical"
            onFinish={onChangePassword}
            autoComplete="off"
          >
            <Form.Item
              label="Current password"
              name="current_password"
              rules={[{ required: true, message: "Required" }]}
            >
              <Input.Password autoComplete="current-password" />
            </Form.Item>
            <Form.Item
              label="New password"
              name="password"
              rules={[
                { required: true, message: "Required" },
                { min: 10, message: "At least 10 characters" },
              ]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
            <Form.Item style={{ marginBottom: 0 }}>
              <Button type="primary" htmlType="submit" loading={submitting}>
                Update password
              </Button>
            </Form.Item>
          </Form>
        </Card>
      </Space>
    </div>
  );
}
