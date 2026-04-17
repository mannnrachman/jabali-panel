// Admin-side "Create database" form. Backend accepts only the bare name
// segment — non-admin users get their username auto-prefixed on the
// server (see panel-api/internal/api/databases.go). Admins create without
// a prefix. The helper text reflects that distinction.
import { useForm, Create } from "@refinedev/antd";
import { Form, Input } from "antd";

type DatabaseCreateInput = {
  name: string;
};

export const DatabaseCreate = () => {
  const { formProps, saveButtonProps } = useForm<DatabaseCreateInput>({
    resource: "databases",
    action: "create",
  });

  return (
    <Create saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical">
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
          tooltip="Admins create databases without a username prefix. When a non-admin user creates one, the server prepends their username automatically (e.g. `alice_wp`)."
        >
          <Input placeholder="e.g. wp_prod" autoComplete="off" />
        </Form.Item>
      </Form>
    </Create>
  );
};
