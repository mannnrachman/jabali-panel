// Create user — Refine useForm() wires up the "submit & navigate" flow
// so we only own the fields. Validation rules mirror the server's
// (email format + password min=10) so the form can reject early without
// a round-trip.
import { Create, useForm, useSelect } from "@refinedev/antd";
import { Form, Input, Switch, Select } from "antd";

type HostingPackage = {
  id: string;
  name: string;
};

type UserCreateInput = {
  email: string;
  password: string;
  name_first?: string;
  name_last?: string;
  is_admin: boolean;
  package_id?: string;
};

export const UserCreate = () => {
  const { formProps, saveButtonProps } = useForm<UserCreateInput>({
    resource: "users",
    action: "create",
  });

  return (
    <Create saveButtonProps={saveButtonProps}>
      <Form {...formProps} layout="vertical" initialValues={{ is_admin: false }}>
        <Form.Item
          label="Email"
          name="email"
          rules={[
            { required: true, message: "Email is required" },
            { type: "email", message: "Must be a valid email" },
          ]}
        >
          {/*
            autoComplete="off" (not "email"): this is an admin creating
            another user's account, so browsers auto-filling the logged-in
            admin's own email is wrong UX — the admin isn't the new user.
            Also suppresses a Chromium autofill race with Playwright's
            .fill() that made the users-spec create/edit/delete flows
            intermittently fail (the adjacent Password's new-password
            autocomplete triggers password-manager heuristics that clear
            the email input asynchronously).
          */}
          <Input autoComplete="off" />
        </Form.Item>

        <Form.Item
          label="Password"
          name="password"
          tooltip="At least 10 characters"
          rules={[
            { required: true, message: "Password is required" },
            { min: 10, message: "Password must be at least 10 characters" },
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>

        <Form.Item label="First name" name="name_first">
          <Input />
        </Form.Item>

        <Form.Item label="Last name" name="name_last">
          <Input />
        </Form.Item>

        <Form.Item
          label="Admin"
          name="is_admin"
          valuePropName="checked"
          tooltip="Admins can see and manage all users."
        >
          <Switch />
        </Form.Item>

        <Form.Item label="Hosting package" name="package_id">
          <PackageSelect />
        </Form.Item>
      </Form>
    </Create>
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
