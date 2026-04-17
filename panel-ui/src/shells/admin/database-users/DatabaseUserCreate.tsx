// Admin create form for a database user.
//
// Contract with panel-api (see database_users.go):
//   POST /database-users  { database_id, username, grant_level }
//     → { id, username, password, grant: {id, grant_level} }
//
// The password is returned ONCE and shown via the reveal-once modal.
// Once the operator closes the modal the password is gone for good.
import { useState } from "react";
import { Create } from "@refinedev/antd";
import { Button, Form, Input, Radio, Select, Space, Typography, message } from "antd";
import { useNavigate } from "react-router";
import { useList } from "@refinedev/core";

import { apiClient } from "../../../apiClient";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";

type CreateInput = {
  database_id: string;
  username: string;
  grant_level: "rw" | "ro";
};

type CreateResponse = {
  id: string;
  username: string;
  password: string;
  grant: { id: string; grant_level: string };
};

type DatabaseOption = { id: string; name: string };

export const DatabaseUserCreate = () => {
  const navigate = useNavigate();
  const [form] = Form.useForm<CreateInput>();
  const [submitting, setSubmitting] = useState(false);
  const [revealed, setRevealed] = useState<{
    username: string;
    password: string;
  } | null>(null);

  // Fetch up to 200 databases for the picker. The admin view is
  // unscoped; a large deployment should get a searchable async-select
  // here, but at this scale a flat list is clearer.
  const { data: dbData, isLoading: dbLoading } = useList<DatabaseOption>({
    resource: "databases",
    pagination: { pageSize: 200 },
  });
  const databases = dbData?.data ?? [];

  const onFinish = async (values: CreateInput) => {
    setSubmitting(true);
    try {
      const resp = await apiClient.post<CreateResponse>(
        "/database-users",
        values,
      );
      setRevealed({ username: resp.data.username, password: resp.data.password });
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ?? "Failed to create database user";
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const onPasswordDismissed = () => {
    setRevealed(null);
    // DB Users list is stacked under /databases; navigate back there
    // rather than the standalone /database-users path, which has no
    // page component (resource is hidden from the sidebar).
    // Works for both shells: from /jabali-{admin,panel}/database-users/create
    // '../../databases' → /jabali-{admin,panel}/databases.
    navigate("../../databases");
  };

  return (
    <>
      <Create
        saveButtonProps={{
          loading: submitting,
          onClick: () => form.submit(),
        }}
      >
        <Form<CreateInput>
          form={form}
          layout="vertical"
          initialValues={{ grant_level: "rw" }}
          onFinish={onFinish}
        >
          <Form.Item
            label="Database"
            name="database_id"
            rules={[{ required: true, message: "Pick a database" }]}
          >
            <Select<string>
              loading={dbLoading}
              showSearch
              optionFilterProp="label"
              placeholder="Select a database"
              options={databases.map((d) => ({ value: d.id, label: d.name }))}
            />
          </Form.Item>

          <Form.Item
            label="Username"
            name="username"
            rules={[
              { required: true, message: "Username is required" },
              {
                pattern: /^[a-z][a-z0-9_]{0,30}$/,
                message:
                  "Lowercase letters, digits and underscores only; must start with a letter; max 30 chars",
              },
            ]}
            tooltip="For non-admin users the server prepends your account's username (e.g. `alice_api`). Admins create without a prefix."
          >
            <Input placeholder="e.g. api" autoComplete="off" />
          </Form.Item>

          <Form.Item
            label="Grant level"
            name="grant_level"
            rules={[{ required: true }]}
            extra={
              <Typography.Text type="secondary">
                rw: full read/write. ro: read-only (SELECT + SHOW VIEW).
              </Typography.Text>
            }
          >
            <Radio.Group>
              <Radio.Button value="rw">Read / Write</Radio.Button>
              <Radio.Button value="ro">Read-only</Radio.Button>
            </Radio.Group>
          </Form.Item>

          <Space>
            <Button onClick={() => navigate(-1)}>Cancel</Button>
          </Space>
        </Form>
      </Create>

      <DatabaseUserPasswordModal
        open={revealed !== null}
        username={revealed?.username ?? ""}
        password={revealed?.password ?? ""}
        title="Database user created"
        onClose={onPasswordDismissed}
      />
    </>
  );
};
