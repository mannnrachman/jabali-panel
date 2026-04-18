// Quick Database Setup modal: creates a database + user + grants ALL
// privileges on that database in one click. Three sequential API calls:
//
//   1. POST /databases             -> {id, name, ...}
//   2. POST /database-users        -> {id, username, password}   // password is shown once
//   3. POST /database-users/:id/grants  {database_id, privileges: ["ALL"]}
//
// Rollback: if step 2 fails we leave the DB in place and surface it to
// the user (they can delete it); if step 3 fails we leave DB + user and
// surface the partial credentials so the user can grant manually.
// Atomic rollback would need a new backend endpoint — out of scope for
// the shortcut.

import { useState } from "react";
import {
  Modal,
  Form,
  Input,
  Button,
  Space,
  Typography,
  message,
  Alert,
  Tooltip,
} from "antd";
import { CopyOutlined, CheckCircleTwoTone } from "@ant-design/icons";
import { apiClient } from "../../../apiClient";

type Props = {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
};

type CreatedResult = {
  databaseName: string;
  username: string;
  password: string;
};

type ApiError = {
  response?: { data?: { error?: string; detail?: string } };
  message?: string;
};

function extractError(err: unknown, fallback: string): string {
  const e = err as ApiError;
  return (
    e.response?.data?.detail ??
    e.response?.data?.error ??
    e.message ??
    fallback
  );
}

export const QuickSetupModal = ({ open, onClose, onSuccess }: Props) => {
  const [form] = Form.useForm<{ name: string }>();
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<CreatedResult | null>(null);

  const reset = () => {
    form.resetFields();
    setResult(null);
  };

  const handleClose = () => {
    reset();
    onClose();
  };

  const handleSubmit = async () => {
    try {
      await form.validateFields();
    } catch {
      return;
    }
    const { name } = form.getFieldsValue();
    setSubmitting(true);
    try {
      const dbResp = await apiClient.post<{ id: string; name: string }>(
        "/databases",
        { name },
      );
      const dbId = dbResp.data.id;
      const dbName = dbResp.data.name;

      let userId: string;
      let username: string;
      let password: string;
      try {
        const userResp = await apiClient.post<{
          id: string;
          username: string;
          password: string;
        }>("/database-users", { username: name });
        userId = userResp.data.id;
        username = userResp.data.username;
        password = userResp.data.password;
      } catch (err) {
        message.error(
          `Database "${dbName}" was created, but user creation failed: ${extractError(err, "unknown")}. You can delete the database from the list.`,
        );
        onSuccess();
        return;
      }

      try {
        await apiClient.post(`/database-users/${userId}/grants`, {
          database_id: dbId,
          privileges: ["ALL"],
        });
      } catch (err) {
        message.warning(
          `Database "${dbName}" and user "${username}" were created, but the grant failed: ${extractError(err, "unknown")}. You can add the grant manually.`,
        );
        setResult({ databaseName: dbName, username, password });
        onSuccess();
        return;
      }

      setResult({ databaseName: dbName, username, password });
      onSuccess();
    } catch (err) {
      message.error(extractError(err, "Failed to create database"));
    } finally {
      setSubmitting(false);
    }
  };

  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      message.success(`${label} copied`);
    } catch {
      message.error(`Could not copy ${label.toLowerCase()}`);
    }
  };

  return (
    <Modal
      title="Quick Database Setup"
      open={open}
      onCancel={handleClose}
      maskClosable={!submitting && !result}
      footer={
        result
          ? [
              <Button key="done" type="primary" onClick={handleClose}>
                Done
              </Button>,
            ]
          : [
              <Button key="cancel" onClick={handleClose} disabled={submitting}>
                Cancel
              </Button>,
              <Button
                key="submit"
                type="primary"
                loading={submitting}
                onClick={handleSubmit}
              >
                Create Database & User
              </Button>,
            ]
      }
      destroyOnClose
    >
      {!result && (
        <>
          <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
            Create a database and user with full access in one step.
          </Typography.Paragraph>
          <Form form={form} layout="vertical" disabled={submitting}>
            <Form.Item
              label="Database & User Name"
              name="name"
              rules={[
                { required: true, message: "Name is required" },
                {
                  pattern: /^[a-z][a-z0-9_]{0,30}$/,
                  message:
                    "Lowercase letters, digits, and underscores; must start with a letter; max 30 chars",
                },
              ]}
              extra="Your username prefix is added automatically (e.g. alice_wp)."
            >
              <Input placeholder="e.g. wp_prod" autoComplete="off" />
            </Form.Item>
          </Form>
        </>
      )}

      {result && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            icon={<CheckCircleTwoTone twoToneColor="#52c41a" />}
            message="Database and user created"
            description="Copy the password now — it is shown only once. We store only a bcrypt hash."
          />
          <div>
            <Typography.Text strong>Database</Typography.Text>
            <Input
              readOnly
              value={result.databaseName}
              addonAfter={
                <Tooltip title="Copy">
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    onClick={() => copy("Database name", result.databaseName)}
                  />
                </Tooltip>
              }
            />
          </div>
          <div>
            <Typography.Text strong>Username</Typography.Text>
            <Input
              readOnly
              value={result.username}
              addonAfter={
                <Tooltip title="Copy">
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    onClick={() => copy("Username", result.username)}
                  />
                </Tooltip>
              }
            />
          </div>
          <div>
            <Typography.Text strong>Password</Typography.Text>
            <Input.Password
              readOnly
              value={result.password}
              visibilityToggle
              addonAfter={
                <Tooltip title="Copy">
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    onClick={() => copy("Password", result.password)}
                  />
                </Tooltip>
              }
            />
          </div>
        </Space>
      )}
    </Modal>
  );
};
