// AddGrantModal — per-user grant creation.
//
// Given a database user (by id + display username), lets the operator
// pick a database and a grant level (rw → "Full Access", ro → "Read
// only") and posts to /database-users/:id/grants. The current tranche
// supports only the two coarse levels; a future tranche replaces this
// with the granular privilege checkbox set (SELECT/INSERT/UPDATE/…).
import { useEffect, useState } from "react";
import { Alert, Form, Modal, Radio, Select, message } from "antd";
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

type AddGrantInput = {
  database_id: string;
  grant_level: "rw" | "ro";
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
      form.setFieldsValue({ grant_level: "rw" });
    }
  }, [open, form]);

  const onFinish = async (values: AddGrantInput) => {
    if (!userId) return;
    setSubmitting(true);
    try {
      await apiClient.post(`/database-users/${userId}/grants`, values);
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
        initialValues={{ grant_level: "rw" }}
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

        <Form.Item label="Privilege Type" name="grant_level" rules={[{ required: true }]}>
          <Radio.Group>
            <Radio value="rw">Full Access</Radio>
            <Radio value="ro">Read only</Radio>
          </Radio.Group>
        </Form.Item>
      </Form>
    </Modal>
  );
}
