// UserDomainCreate — tenant form for adding a domain to their own
// account. The server auto-fills user_id from the Kratos session, so
// the form only needs a name.
import { Button, Card, Form, Input, Typography, message } from "antd";
import { useNavigate } from "react-router";

import { useCreateMutation } from "../../../hooks/useQueries";

export type UserDomainCreateInput = {
  name: string;
};

type DomainCreated = { id: string };

export const UserDomainCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<UserDomainCreateInput>();
  const createMutation = useCreateMutation<DomainCreated, UserDomainCreateInput>({
    resource: "domains",
  });

  const handleFinish = async (values: UserDomainCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("Domain added");
      // Always land the user back on their own list, never the admin list.
      navigate("/jabali-panel/domains");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to add domain";
      message.error(msg);
    }
  };

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Add domain
      </Typography.Title>
      <Form<UserDomainCreateInput>
        form={form}
        layout="vertical"
        onFinish={handleFinish}
      >
        <Form.Item
          label="Domain Name"
          name="name"
          rules={[{ required: true, message: "Domain name is required" }]}
        >
          <Input placeholder="e.g., example.com" />
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
