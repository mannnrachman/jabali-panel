// UserDomainDrawer — tenant Add-domain Drawer (replaces the
// /jabali-panel/domains/create page route).
import { Button, Drawer, Form, Grid, Input, Space, message } from "antd";
import { useEffect } from "react";

import { useCreateMutation } from "../../../hooks/useQueries";

type UserDomainCreateInput = { name: string };
type DomainCreated = { id: string };

export interface UserDomainDrawerProps {
  open: boolean;
  onClose: () => void;
}

export const UserDomainDrawer = ({ open, onClose }: UserDomainDrawerProps) => {
  const [form] = Form.useForm<UserDomainCreateInput>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg ?? (typeof window !== "undefined" ? window.innerWidth >= 992 : true);

  const createMutation = useCreateMutation<DomainCreated, UserDomainCreateInput>({
    resource: "domains",
  });

  useEffect(() => {
    if (open) form.resetFields();
  }, [open, form]);

  const handleFinish = async (values: UserDomainCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("Domain added");
      onClose();
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Failed to add domain");
    }
  };

  return (
    <Drawer
      title="Add domain"
      open={open}
      onClose={onClose}
      width={isDesktop ? 480 : undefined}
      placement="right"
      destroyOnClose
    >
      <Form<UserDomainCreateInput> form={form} layout="vertical" onFinish={handleFinish}>
        <Form.Item
          label="Domain Name"
          name="name"
          rules={[
            { required: true, message: "Domain name is required" },
            { max: 253, message: "Domain name cannot exceed 253 characters" },
            {
              pattern: /^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$/,
              message: "Enter a valid domain name (e.g. example.com)",
            },
          ]}
        >
          <Input placeholder="e.g., example.com" />
        </Form.Item>

        <Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" loading={createMutation.isPending}>
              Add
            </Button>
            <Button onClick={onClose}>Cancel</Button>
          </Space>
        </Form.Item>
      </Form>
    </Drawer>
  );
};
