// Create-user form — username only.
//
// The user is provisioned as a MariaDB account with a random password
// returned exactly once. Database access is granted in a separate
// step from the user row's "Add Access" action (see AddGrantModal).
// This mirrors the standard shared-hosting model: one user can hold
// any number of grants across different databases.
import { useState } from "react";
import { Button, Card, Form, Input, Space, Typography, message } from "antd";
import { useNavigate } from "react-router";

import { apiClient } from "../../../apiClient";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";

type CreateInput = {
  username: string;
};

type CreateResponse = {
  id: string;
  username: string;
  password: string;
};

export const DatabaseUserCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<CreateInput>();
  const [submitting, setSubmitting] = useState(false);
  const [revealed, setRevealed] = useState<{
    username: string;
    password: string;
  } | null>(null);

  const onFinish = async (values: CreateInput) => {
    setSubmitting(true);
    try {
      const resp = await apiClient.post<CreateResponse>(
        "/database-users",
        values,
      );
      setRevealed({
        username: resp.data.username,
        password: resp.data.password,
      });
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ?? "Failed to create database user";
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const onPasswordDismissed = () => {
    setRevealed(null);
    // From /jabali-{admin,panel}/database-users/create, '../../databases'
    // lands on the combined databases page where the new row appears.
    navigate("../../databases");
  };

  return (
    <>
      <Card>
        <Typography.Title level={3} style={{ marginTop: 0 }}>
          Create database user
        </Typography.Title>
        <Form<CreateInput>
          form={form}
          layout="vertical"
          onFinish={onFinish}
        >
          <Form.Item
            label="Username"
            name="username"
            rules={[
              { required: true, message: "Username is required" },
              {
                pattern: /^[a-z][a-z0-9_]{0,30}$/,
                message:
                  "Lowercase letters, digits and underscores only; must start with a letter; max 30 chars",
              },
            ]}
            tooltip="The final MariaDB username will be your panel username plus an underscore plus this value (e.g. alice_api)."
            extra="Your username will be prepended automatically."
          >
            <Input placeholder="e.g. api" autoComplete="off" />
          </Form.Item>

          <Form.Item>
            <Space>
              <Button
                type="primary"
                htmlType="submit"
                loading={submitting}
              >
                Save
              </Button>
              <Button onClick={() => navigate(-1)}>Cancel</Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>

      <DatabaseUserPasswordModal
        open={revealed !== null}
        username={revealed?.username ?? ""}
        password={revealed?.password ?? ""}
        title="Database user created"
        onClose={onPasswordDismissed}
      />
    </>
  );
};
