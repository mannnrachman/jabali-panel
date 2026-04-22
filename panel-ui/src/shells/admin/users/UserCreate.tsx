// Create user — Form.useForm + useCreateMutation wire the "submit &
// navigate" flow so we only own the fields. Validation rules mirror
// the server's (email format + password min=10) so the form can
// reject early without a round-trip.
import { Button, Card, Form, Input, Select, Switch, Typography, message } from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";
import { useNavigate } from "react-router";

import { PasswordInput } from "../../../components/PasswordInput";
import { useCreateMutation } from "../../../hooks/useQueries";
import { useSelectQuery } from "../../../hooks/useSelectQuery";

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

type UserCreated = {
  id: string;
};

export const UserCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<UserCreateInput>();
  const createMutation = useCreateMutation<UserCreated, UserCreateInput>({
    resource: "users",
  });

  const handleFinish = async (values: UserCreateInput) => {
    try {
      await createMutation.mutateAsync(values);
      message.success("User created");
      navigate("/jabali-admin/users");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to create user";
      message.error(msg);
    }
  };

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Create user
      </Typography.Title>
      <Form<UserCreateInput>
        form={form}
        layout="vertical"
        initialValues={{ is_admin: false }}
        onFinish={handleFinish}
      >
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
          <PasswordInput autoComplete="new-password" />
        </Form.Item>

        <Form.Item label="First name" name="name_first">
          <Input />
        </Form.Item>

        <Form.Item label="Last name" name="name_last">
          <Input />
        </Form.Item>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 24 }}>
          <Form.Item
            name="is_admin"
            valuePropName="checked"
            tooltip="Admins can see and manage all users."
            noStyle
          >
            <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
          </Form.Item>
          <Typography.Text>Admin</Typography.Text>
        </div>

        <Form.Item label="Hosting package" name="package_id">
          <PackageSelect />
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

const PackageSelect = (props: {
  value?: string | null;
  onChange?: (v: string | null) => void;
}) => {
  const { options, isLoading } = useSelectQuery<HostingPackage>({
    resource: "packages",
    labelField: "name",
    valueField: "id",
  });

  return (
    <Select
      placeholder="Select a package (optional)"
      allowClear
      loading={isLoading}
      options={[{ label: "No package", value: null }, ...options]}
      value={props.value}
      onChange={(v) => props.onChange?.((v ?? null) as string | null)}
    />
  );
};
