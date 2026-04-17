// User-side "Create database" form. The backend prepends the caller's
// username to the final MariaDB database name (e.g. `alice_wp`) — this
// form just accepts the unprefixed suffix, and the tooltip explains
// what will actually be created.
//
// Redirect: Refine's built-in `redirect: "list"` infers the target from
// the resource's registered `list` path, but the hosted databases
// resource is registered under name "user-databases" while useForm uses
// resource "databases" (the API slug). To get a reliable redirect
// regardless of that mismatch, we handle navigation explicitly with
// useNavigate and go to the absolute user-shell path after success.
import { useForm, Create } from "@refinedev/antd";
import { Form, Input } from "antd";
import { useNavigate } from "react-router";

type UserDatabaseCreateInput = {
  name: string;
};

export const UserDatabaseCreate = () => {
  const navigate = useNavigate();
  const { formProps, saveButtonProps } = useForm<UserDatabaseCreateInput>({
    resource: "databases",
    action: "create",
    redirect: false,
    onMutationSuccess: () => {
      navigate("/jabali-panel/databases");
    },
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
