// User-side "Create database" form. The backend prepends the caller's
// username to the final MariaDB database name (e.g. `alice_wp`) —
// this form just accepts the unprefixed suffix, and the tooltip
// explains what will actually be created.
import { Button, Card, Form, Input, Typography, message } from "antd";
import { useNavigate } from "react-router";

import { useCreateMutation } from "../../../hooks/useQueries";

type UserDatabaseCreateInput = {
  name: string;
};

type DatabaseCreated = { id: string };

export const UserDatabaseCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<UserDatabaseCreateInput>();
  const createMutation = useCreateMutation<
    DatabaseCreated,
    UserDatabaseCreateInput
  >({
    resource: "databases",
  });

  const handleFinish = async (values: UserDatabaseCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("Database created");
      // Absolute path — we never want the user-shell form to land
      // the user on the admin list (which would bounce via
      // RequireAdmin anyway).
      navigate("/jabali-panel/databases");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to create database";
      message.error(msg);
    }
  };

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Create database
      </Typography.Title>
      <Form<UserDatabaseCreateInput>
        form={form}
        layout="vertical"
        onFinish={handleFinish}
      >
        <Form.Item
          label="Name"
          name="name"
          rules={[
            { required: true, message: "Database name is required" },
            {
              pattern: /^[a-z][a-z0-9_]{0,30}$/,
              message:
                "Lowercase letters, digits and underscores only; must start with a letter; max 30 chars",
            },
          ]}
          tooltip="The final database name is your username plus an underscore plus this suffix (e.g. `alice_wp`)."
          extra="Your username will be prepended automatically."
        >
          <Input placeholder="e.g. wp_prod" autoComplete="off" />
        </Form.Item>

        <Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={createMutation.isPending}
          >
            Save
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
};
