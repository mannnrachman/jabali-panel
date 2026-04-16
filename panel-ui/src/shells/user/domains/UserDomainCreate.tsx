import { useForm } from "@refinedev/antd";
import { Create } from "@refinedev/antd";
import { Form, Input } from "antd";
import { useNavigate } from "react-router";

export type UserDomainCreateInput = {
  name: string;
};

export const UserDomainCreate = () => {
  const navigate = useNavigate();
  // The `domains` resource is registered with admin-shell paths
  // (/jabali-admin/domains). Refine's default post-create redirect
  // would send the user shell to the admin list, which the router
  // denies and bounces to the dashboard. Disable the built-in
  // redirect and send the user to their own list explicitly.
  const { formProps, saveButtonProps } = useForm<UserDomainCreateInput>({
    resource: "domains",
    action: "create",
    redirect: false,
    onMutationSuccess: () => {
      navigate("/jabali-panel/domains");
    },
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
