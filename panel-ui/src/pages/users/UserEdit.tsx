// Edit user. useForm in 'edit' mode auto-loads the record via
// dataProvider.getOne(), populates the form, and PATCHes on submit.
//
// Password is optional on edit (leave blank to keep). The
// current_password field only matters when the caller is editing their
// own account and isn't an admin — the server enforces this, we just
// offer the field so the form *can* succeed in that case.
import { Edit, useForm } from "@refinedev/antd";
import { Form, Input, Switch } from "antd";

type UserEditInput = {
  email: string;
  name_first?: string;
  name_last?: string;
  is_admin?: boolean;
  password?: string;
  current_password?: string;
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
        // Strip blank password fields before sending so the server doesn't
        // try to validate / re-hash an empty string.
        onFinish={(raw) => {
          const clean = { ...(raw as UserEditInput) };
          if (!clean.password) delete clean.password;
          if (!clean.current_password) delete clean.current_password;
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

        <Form.Item
          label="New password"
          name="password"
          tooltip="Leave blank to keep current password."
          rules={[{ min: 10, message: "At least 10 characters" }]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>

        <Form.Item
          label="Current password"
          name="current_password"
          tooltip="Only required when changing your own password as a non-admin."
        >
          <Input.Password autoComplete="current-password" />
        </Form.Item>
      </Form>
    </Edit>
  );
};
