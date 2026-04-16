import { useForm } from "@refinedev/antd";
import { Create } from "@refinedev/antd";
import { Form, Input } from "antd";

export type UserDomainCreateInput = {
  name: string;
};

export const UserDomainCreate = () => {
  const { formProps, saveButtonProps } = useForm<UserDomainCreateInput>({
    resource: "domains",
    action: "create",
  });

  return (
    <Create saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
        <Form.Item
          label="Domain Name"
          name="name"
          rules={[{ required: true, message: "Domain name is required" }]}
        >
          <Input placeholder="e.g., example.com" />
        </Form.Item>
      </Form>
    </Create>
  );
};
