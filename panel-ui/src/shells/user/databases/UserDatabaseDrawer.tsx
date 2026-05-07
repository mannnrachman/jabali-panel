// UserDatabaseDrawer — tenant Create-database Drawer (replaces the
// /jabali-panel/databases/create page route). Backend prepends the
// caller's username to the final database name.
import { Button, Drawer, Form, Grid, Input, Segmented, Space, message } from "antd";
import { useEffect } from "react";

import { useCreateMutation } from "../../../hooks/useQueries";

type UserDatabaseCreateInput = { name: string; engine?: "mariadb" | "postgres" };
type DatabaseCreated = { id: string };

export interface UserDatabaseDrawerProps {
  open: boolean;
  onClose: () => void;
}

export const UserDatabaseDrawer = ({ open, onClose }: UserDatabaseDrawerProps) => {
  const [form] = Form.useForm<UserDatabaseCreateInput>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;

  const createMutation = useCreateMutation<DatabaseCreated, UserDatabaseCreateInput>({
    resource: "databases",
  });

  useEffect(() => {
    if (open) form.resetFields();
  }, [open, form]);

  const handleFinish = async (values: UserDatabaseCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("Database created");
      onClose();
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Failed to create database");
    }
  };

  return (
    <Drawer
      title="Create database"
      open={open}
      onClose={onClose}
      width={isDesktop ? 480 : undefined}
      placement="right"
      destroyOnClose
    >
      <Form<UserDatabaseCreateInput>
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={{ engine: "mariadb" }}
      >
        <Form.Item
          label="Engine"
          name="engine"
          tooltip="MariaDB is the default. PostgreSQL must be enabled by an admin in Server Settings."
        >
          <Segmented
            options={[
              { label: "MariaDB", value: "mariadb" },
              { label: "PostgreSQL", value: "postgres" },
            ]}
          />
        </Form.Item>

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
          <Space>
            <Button type="primary" htmlType="submit" loading={createMutation.isPending}>
              Create
            </Button>
            <Button onClick={onClose}>Cancel</Button>
          </Space>
        </Form.Item>
      </Form>
    </Drawer>
  );
};
