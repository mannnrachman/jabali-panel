// UserDrawer — create + edit drawer for admin Users page.
//
// Replaces the standalone UserCreate / UserEdit page routes with a
// right-side Drawer (matches the docs/CONVENTIONS.md "Drawer for
// create+edit" pattern used by AdminChannelDrawer).
//
// Validation rules mirror the server's so the form rejects early.
// Password on edit is optional — blank means "keep current".
import { Button, Drawer, Form, Grid, Input, Space, Spin, Switch, message } from "antd";
import { useEffect } from "react";

import { CheckOutlined, CloseOutlined } from "@icons";

import { PasswordInput } from "../../../components/PasswordInput";
import {
  useCreateMutation,
  useOneQuery,
  useUpdateMutation,
} from "../../../hooks/useQueries";
import { useSelectQuery } from "../../../hooks/useSelectQuery";

type HostingPackage = {
  id: string;
  name: string;
};

type UserFormInput = {
  email: string;
  username?: string;
  password?: string;
  name_first?: string;
  name_last?: string;
  is_admin: boolean;
  package_id?: string | null;
};

type UserRecord = UserFormInput & {
  id: string;
};

type UserCreated = { id: string };

export interface UserDrawerProps {
  open: boolean;
  onClose: () => void;
  /** Existing user id for edit mode. Undefined = create. */
  editingId?: string;
}

const RESOURCE = "users";

export function UserDrawer({ open, onClose, editingId }: UserDrawerProps) {
  const [form] = Form.useForm<UserFormInput>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;
  const isEdit = Boolean(editingId);

  const { data: existing, isLoading } = useOneQuery<UserRecord>({
    resource: RESOURCE,
    id: editingId,
    enabled: isEdit && open,
  });

  const create = useCreateMutation<UserCreated, UserFormInput>({ resource: RESOURCE });
  const update = useUpdateMutation<UserRecord, UserFormInput>({ resource: RESOURCE });

  useEffect(() => {
    if (!open) return;
    if (isEdit && existing) {
      // Drop password so the edit field stays empty (blank = keep).
      const { password: _pw, ...rest } = existing;
      void _pw;
      form.resetFields();
      form.setFieldsValue(rest);
    } else if (!isEdit) {
      form.resetFields();
      form.setFieldsValue({ is_admin: false });
    }
  }, [open, isEdit, existing, form]);

  const handleFinish = async (values: UserFormInput) => {
    try {
      if (isEdit && editingId) {
        const payload = { ...values };
        if (!payload.password) delete payload.password;
        await update.mutateAsync({ id: editingId, input: payload });
        message.success("User updated");
      } else {
        await create.mutateAsync(values);
        message.success("User created");
      }
      onClose();
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Save failed");
    }
  };

  return (
    <Drawer
      title={isEdit ? "Edit user" : "Create user"}
      open={open}
      onClose={onClose}
      width={isDesktop ? 520 : undefined}
      placement="right"
      destroyOnClose
    >
      {isEdit && isLoading && !existing ? (
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
      ) : (
        <Form<UserFormInput>
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
            <Input autoComplete={isEdit ? "email" : "off"} />
          </Form.Item>

          {!isEdit && (
            <Form.Item
              label="Username"
              name="username"
              tooltip="Linux account name. Lowercase letters and digits, 3–32 chars, must start with a letter. Leave blank for admin-only accounts."
              dependencies={["is_admin"]}
              rules={[
                ({ getFieldValue }) => ({
                  validator(_, value: string | undefined) {
                    const isAdmin = getFieldValue("is_admin");
                    if (!isAdmin && !value) {
                      return Promise.reject(
                        new Error("Username is required for non-admin users"),
                      );
                    }
                    if (value && !/^[a-z][a-z0-9]{2,31}$/.test(value)) {
                      return Promise.reject(
                        new Error(
                          "3–32 chars, lowercase letters and digits, must start with a letter",
                        ),
                      );
                    }
                    return Promise.resolve();
                  },
                }),
              ]}
            >
              <Input autoComplete="off" placeholder="e.g. alice, dev42" />
            </Form.Item>
          )}

          <Form.Item
            label={isEdit ? "New password" : "Password"}
            name="password"
            tooltip={isEdit ? "Leave blank to keep current password." : "At least 10 characters."}
            rules={
              isEdit
                ? [{ min: 10, message: "At least 10 characters" }]
                : [
                    { required: true, message: "Password is required" },
                    { min: 10, message: "At least 10 characters" },
                  ]
            }
          >
            <PasswordInput autoComplete="new-password" />
          </Form.Item>

          <Form.Item label="First name" name="name_first">
            <Input />
          </Form.Item>
          <Form.Item label="Last name" name="name_last">
            <Input />
          </Form.Item>

          <Form.Item
            name="is_admin"
            label="Admin"
            valuePropName="checked"
            tooltip="Admins can see and manage all users."
          >
            <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />} />
          </Form.Item>

          <Form.Item label="Hosting package" name="package_id">
            <PackageSelect />
          </Form.Item>

          <Form.Item>
            <Space>
              <Button
                type="primary"
                htmlType="submit"
                loading={create.isPending || update.isPending}
              >
                {isEdit ? "Save" : "Create"}
              </Button>
              <Button onClick={onClose}>Cancel</Button>
            </Space>
          </Form.Item>
        </Form>
      )}
    </Drawer>
  );
}

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

// Re-export the type so list pages can avoid duplicating the shape.
export type { UserRecord };
