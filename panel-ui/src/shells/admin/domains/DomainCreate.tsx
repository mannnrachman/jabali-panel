import { useForm } from "@refinedev/antd";
import { Create } from "@refinedev/antd";
import { Form, Input } from "antd";

export type DomainCreateInput = {
  name: string;
  user_id: string;
  doc_root?: string;
};

export const DomainCreate = () => {
  const { formProps, saveButtonProps } = useForm<DomainCreateInput>({
    resource: "domains",
    action: "create",
  });

  return (
    <Create saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
        <Form.Item
          label="Name"
          name="name"
          rules={[{ required: true, message: "Domain name is required" }]}
        >
          <Input placeholder="e.g., example.com" />
        </Form.Item>

        <Form.Item
          label="User ID"
          name="user_id"
          rules={[{ required: true, message: "User ID is required" }]}
        >
          <Input placeholder="User ID (will be a Select later)" />
        </Form.Item>

        <Form.Item
          label="Doc Root"
          name="doc_root"
          tooltip="Leave empty for auto-generated path"
        >
          <Input placeholder="auto-generated if empty" />
        </Form.Item>
      </Form>
    </Create>
  );
};
