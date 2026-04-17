// User-side "Create database" form. The backend prepends the caller's
// username to the final MariaDB database name (e.g. `alice_wp`) — this
// form just accepts the unprefixed suffix, and the tooltip explains
// what will actually be created.
import { useForm, Create } from "@refinedev/antd";
import { Form, Input } from "antd";

type UserDatabaseCreateInput = {
  name: string;
};

export const UserDatabaseCreate = () => {
  const { formProps, saveButtonProps } = useForm<UserDatabaseCreateInput>({
    resource: "databases",
    action: "create",
    // On success, navigate back to the user's own databases list rather
    // than the default admin list that Refine infers from resource name.
    redirect: "list",
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
          tooltip="The final database name is your username plus an underscore plus this suffix (e.g. `alice_wp`)."
          extra="Your username will be prepended automatically."
        >
          <Input placeholder="e.g. wp_prod" autoComplete="off" />
        </Form.Item>
      </Form>
    </Create>
  );
};
