// AddGrantModal — per-user grant creation.
//
// Given a database user (by id + display username), lets the operator
// pick a database and either use a preset grant level (rw → "Full Access",
// ro → "Read only") or custom privileges. Supports granular privilege
// checkbox set (SELECT/INSERT/UPDATE/DELETE/CREATE/DROP/ALTER/INDEX).
import { useEffect, useState } from "react";
import { Alert, Form, Modal, Radio, Select, Checkbox, Space, message } from "antd";
import { useList } from "@refinedev/core";

import { apiClient } from "../apiClient";

interface AddGrantModalProps {
  open: boolean;
  userId: string | null;
  username: string;
  /** Database ids this user already has a grant on — pre-filtered in the picker. */
  excludedDatabaseIds: string[];
  onClose: () => void;
  /** Called after a successful grant POST so the parent can refresh its table. */
  onSuccess: () => void;
}

type DatabaseOption = { id: string; name: string };

const AVAILABLE_PRIVILEGES = ["SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "INDEX"];

type AddGrantInput = {
  database_id: string;
  grantType: "preset" | "custom";
  grant_level?: "rw" | "ro";
  privileges?: string[];
};

export function AddGrantModal({
  open,
  userId,
  username,
  excludedDatabaseIds,
  onClose,
  onSuccess,
}: AddGrantModalProps) {
  const [form] = Form.useForm<AddGrantInput>();
  const [submitting, setSubmitting] = useState(false);
  const grantType = Form.useWatch("grantType", form);

  // Fresh list of databases each open — 200 is plenty for a
  // single-tenant panel; large installs can move to an async select.
  const { data: dbData, isLoading } = useList<DatabaseOption>({
    resource: "databases",
    pagination: { pageSize: 200 },
    queryOptions: { enabled: open },
  });
  const databases = (dbData?.data ?? []).filter(
    (d) => !excludedDatabaseIds.includes(d.id),
  );

  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue({ grantType: "preset", grant_level: "rw", privileges: [] });
    }
  }, [open, form]);

  const onFinish = async (values: AddGrantInput) => {
    if (!userId) return;
    setSubmitting(true);
    try {
      // Build the request: send privileges array if custom, otherwise send grant_level
      const payload: Record<string, any> = {
        database_id: values.database_id,
      };

      if (values.grantType === "custom" && values.privileges && values.privileges.length > 0) {
        payload.privileges = values.privileges;
      } else {
        payload.grant_level = values.grant_level || "rw";
      }

      await apiClient.post(`/database-users/${userId}/grants`, payload);
      message.success("Access granted");
      onSuccess();
      onClose();
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ?? "Failed to grant access";
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      title="Add Database Access"
      open={open}
      onCancel={onClose}
      okText="Grant Access"
      okButtonProps={{ loading: submitting, onClick: () => form.submit() }}
      destroyOnClose
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={`Grant privileges to ${username || "—"}@localhost`}
      />

      <Form<AddGrantInput>
        form={form}
        layout="vertical"
        initialValues={{ grantType: "preset", grant_level: "rw", privileges: [] }}
        onFinish={onFinish}
      >
        <Form.Item
          label="Database"
          name="database_id"
          rules={[{ required: true, message: "Pick a database" }]}
        >
          <Select<string>
            loading={isLoading}
            showSearch
            optionFilterProp="label"
            placeholder="Select a database"
            options={databases.map((d) => ({ value: d.id, label: d.name }))}
            notFoundContent={
              excludedDatabaseIds.length > 0 && databases.length === 0
                ? "User already has access to every database."
                : undefined
            }
          />
        </Form.Item>

        <Form.Item label="Grant Type" name="grantType" rules={[{ required: true }]}>
          <Radio.Group>
            <Space direction="vertical" style={{ width: "100%" }}>
              <Radio value="preset">Preset Privileges</Radio>
              <Radio value="custom">Custom Privileges</Radio>
            </Space>
          </Radio.Group>
        </Form.Item>

        {grantType === "preset" && (
          <Form.Item label="Privilege Level" name="grant_level" rules={[{ required: true }]}>
            <Radio.Group>
              <Space direction="vertical">
                <Radio value="rw">Full Access (all privileges)</Radio>
                <Radio value="ro">Read Only (SELECT only)</Radio>
              </Space>
            </Radio.Group>
          </Form.Item>
        )}

        {grantType === "custom" && (
          <Form.Item label="Custom Privileges" name="privileges">
            <Checkbox.Group
              options={AVAILABLE_PRIVILEGES.map((p) => ({ label: p, value: p }))}
            />
          </Form.Item>
        )}
      </Form>
    </Modal>
  );
}
