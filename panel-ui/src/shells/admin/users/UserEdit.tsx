// Edit user — admin-only page. Only admins can reach this route (URL
// sits under /jabali-admin/* which is gated by RoleGate + Authenticated),
// so we never need the current_password field here: admins can reset
// any user's password without proving the old one.
//
// Users changing their OWN password go through /jabali-panel/profile,
// which hits the same PATCH /users/:id endpoint with current_password.
//
// Password is optional on edit — a blank field means "keep current".
import { Edit, useForm, useSelect } from "@refinedev/antd";
import { Form, Input, Switch, Select } from "antd";

type HostingPackage = {
  id: string;
  name: string;
};

type UserEditInput = {
  email: string;
  name_first?: string;
  name_last?: string;
  is_admin?: boolean;
  password?: string;
  package_id?: string;
};

export const UserEdit = () => {
  const { formProps, saveButtonProps } = useForm<UserEditInput>({
    resource: "users",
    action: "edit",
  });

  return (
    <Edit saveButtonProps={saveButtonProps}>
      <Form
        {...formProps}
        layout="vertical"
        // Strip blank password before sending so the server doesn't
        // try to validate / re-hash an empty string.
        onFinish={(raw) => {
          const clean = { ...(raw as UserEditInput) };
          if (!clean.password) delete clean.password;
          return formProps.onFinish?.(clean);
        }}
      >
        <Form.Item
          label="Email"
          name="email"
          rules={[
            { required: true, message: "Email is required" },
            { type: "email", message: "Must be a valid email" },
          ]}
        >
          <Input autoComplete="email" />
        </Form.Item>

        <Form.Item label="First name" name="name_first">
          <Input />
        </Form.Item>
        <Form.Item label="Last name" name="name_last">
          <Input />
        </Form.Item>

        <Form.Item label="Admin" name="is_admin" valuePropName="checked">
          <Switch />
        </Form.Item>

        <Form.Item label="Hosting package" name="package_id">
          <PackageSelect />
        </Form.Item>

        <Form.Item
          label="New password"
          name="password"
          tooltip="Leave blank to keep current password."
          rules={[{ min: 10, message: "At least 10 characters" }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Edit>
  );
};

const PackageSelect = () => {
  const { selectProps } = useSelect<HostingPackage>({
    resource: "packages",
    optionLabel: "name",
    optionValue: "id",
  });

  return (
    <Select
      {...selectProps}
      placeholder="Select a package (optional)"
      allowClear
      options={[
        { label: "No package", value: null },
        ...(selectProps.options ?? []),
      ]}
    />
  );
};
