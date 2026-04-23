// Edit user — admin-only page. Only admins can reach this route (URL
// sits under /jabali-admin/* which is gated by <RequireAdmin>), so we
// never need the current_password field here: admins can reset any
// user's password without proving the old one.
//
// Users changing their OWN password go through /jabali-panel/profile,
// which hits the same PATCH /users/:id endpoint with current_password.
//
// Password is optional on edit — a blank field means "keep current".
import { useEffect } from "react";
import { Button, Card, Form, Input, Select, Spin, Switch, Typography, message } from "antd";
import { CheckOutlined, CloseOutlined } from "@icons";
import { useNavigate, useParams } from "react-router";

import { PasswordInput } from "../../../components/PasswordInput";
import {
  useOneQuery,
  useUpdateMutation,
} from "../../../hooks/useQueries";
import { useSelectQuery } from "../../../hooks/useSelectQuery";

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

type UserRecord = UserEditInput & {
  id: string;
};

export const UserEdit = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [form] = Form.useForm<UserEditInput>();

  const { data, isLoading } = useOneQuery<UserRecord>({
    resource: "users",
    id,
  });

  const updateMutation = useUpdateMutation<UserRecord, UserEditInput>({
    resource: "users",
  });

  useEffect(() => {
    if (data) {
      // Seed the form once the record arrives. We drop `password`
      // so the edit field stays empty (blank means "keep current").
      const { password: _password, ...rest } = data;
      void _password;
      form.setFieldsValue(rest);
    }
  }, [data, form]);

  const handleFinish = async (values: UserEditInput) => {
    if (!id) return;
    // Strip blank password before sending so the server doesn't try
    // to validate / re-hash an empty string.
    const payload = { ...values };
    if (!payload.password) delete payload.password;

    try {
      await updateMutation.mutateAsync({ id, input: payload });
      message.success("User updated");
      navigate("/jabali-admin/users");
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : "Failed to update user";
      message.error(msg);
    }
  };

  if (isLoading && !data) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: 240,
        }}
      >
        <Spin />
      </div>
    );
  }

  return (
    <Card>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Edit user
      </Typography.Title>
      <Form<UserEditInput>
        form={form}
        layout="vertical"
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
          <Input autoComplete="email" />
        </Form.Item>

        <Form.Item label="First name" name="name_first">
          <Input />
        </Form.Item>
        <Form.Item label="Last name" name="name_last">
          <Input />
        </Form.Item>

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 24 }}>
          <Form.Item name="is_admin" valuePropName="checked" noStyle>
            <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
          </Form.Item>
          <Typography.Text>Admin</Typography.Text>
        </div>

        <Form.Item label="Hosting package" name="package_id">
          <PackageSelect />
        </Form.Item>

        <Form.Item
          label="New password"
          name="password"
          tooltip="Leave blank to keep current password."
          rules={[{ min: 10, message: "At least 10 characters" }]}
        >
          <PasswordInput autoComplete="new-password" />
        </Form.Item>

        <Form.Item>
          <Button
            type="primary"
            htmlType="submit"
            loading={updateMutation.isPending}
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
