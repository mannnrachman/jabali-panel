// Install modal — provisions a WordPress site on a domain that doesn't
// already host one. POSTs to /wordpress-installs; backend creates the
// DB + DB-user + grant, spawns an async agent job, and returns 202 with
// the admin password delivered exactly once. We show it in a reveal-
// once panel using the same pattern as Quick Setup.
//
// The domain dropdown excludes any domain already hosting an install
// (passed in via alreadyHosted) because the backend enforces a 1:1
// domain↔install relationship and would 409 otherwise.

import { useState, useEffect } from "react";
import {
  Modal,
  Form,
  Input,
  Button,
  Select,
  Space,
  Typography,
  message,
  Alert,
  Tooltip,
} from "antd";
import { CopyOutlined, CheckCircleTwoTone } from "@ant-design/icons";
import { useInvalidate } from "@refinedev/core";
import { apiClient } from "../../../apiClient";

type Domain = { id: string; name: string };

type Props = {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
  alreadyHostedDomainIds: Set<string>;
  defaultAdminEmail?: string;
};

type CreatedResult = {
  domainName: string;
  adminUsername: string;
  adminEmail: string;
  adminPassword: string;
  dbId: string;
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

// Common WordPress locales. en_US is the default; a tooltip explains
// users can request others via support if they need one not listed.
const LOCALES: { value: string; label: string }[] = [
  { value: "en_US", label: "English (US)" },
  { value: "en_GB", label: "English (UK)" },
  { value: "he_IL", label: "עברית (Hebrew)" },
  { value: "ar",    label: "العربية (Arabic)" },
  { value: "es_ES", label: "Español (Spain)" },
  { value: "fr_FR", label: "Français" },
  { value: "de_DE", label: "Deutsch" },
  { value: "it_IT", label: "Italiano" },
  { value: "pt_BR", label: "Português (Brasil)" },
  { value: "ru_RU", label: "Русский" },
  { value: "ja",    label: "日本語" },
  { value: "zh_CN", label: "简体中文" },
];

export const InstallWordPressModal = ({
  open,
  onClose,
  onSuccess,
  alreadyHostedDomainIds,
  defaultAdminEmail,
}: Props) => {
  const [form] = Form.useForm<{
    domain_id: string;
    site_title: string;
    admin_username: string;
    admin_email: string;
    admin_password: string;
    locale: string;
  }>();
  const [submitting, setSubmitting] = useState(false);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loadingDomains, setLoadingDomains] = useState(false);
  const [result, setResult] = useState<CreatedResult | null>(null);
  const invalidate = useInvalidate();

  // The DB + user cache also gets a new row (install creates both),
  // so invalidate those lists too — matches Quick Setup's behavior.
  const refreshLists = () => {
    invalidate({ resource: "wordpress-installs", invalidates: ["list"] });
    invalidate({ resource: "databases", invalidates: ["list"] });
    invalidate({ resource: "database-users", invalidates: ["list"] });
    onSuccess();
  };

  // Load domains when modal opens. Unwraps paginated envelope —
  // /domains returns {data, total, page, page_size} and a prior
  // regression had us feeding the envelope directly into .map().
  useEffect(() => {
    if (!open) return;
    let alive = true;
    setLoadingDomains(true);
    apiClient
      .get<{ data: Domain[] }>("/domains", { params: { page: 1, page_size: 100 } })
      .then((resp) => {
        if (!alive) return;
        setDomains(resp.data?.data ?? []);
      })
      .catch((err) => {
        if (!alive) return;
        message.error(extractError(err, "Failed to load domains"));
      })
      .finally(() => {
        if (alive) setLoadingDomains(false);
      });
    return () => {
      alive = false;
    };
  }, [open]);

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
    const vals = form.getFieldsValue();
    setSubmitting(true);
    try {
      const resp = await apiClient.post<{
        id: string;
        domain_id: string;
        db_id: string;
        admin_username: string;
        admin_email: string;
        admin_password: string;
      }>("/wordpress-installs", {
        domain_id: vals.domain_id,
        site_title: vals.site_title,
        admin_username: vals.admin_username,
        admin_email: vals.admin_email,
        admin_password: vals.admin_password || undefined,
        locale: vals.locale || "en_US",
      });
      const domainName =
        domains.find((d) => d.id === vals.domain_id)?.name ?? vals.domain_id;
      setResult({
        domainName,
        adminUsername: resp.data.admin_username,
        adminEmail: resp.data.admin_email,
        adminPassword: resp.data.admin_password,
        dbId: resp.data.db_id,
      });
      refreshLists();
    } catch (err) {
      message.error(extractError(err, "Failed to install WordPress"));
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

  const availableDomains = domains.filter(
    (d) => !alreadyHostedDomainIds.has(d.id),
  );

  return (
    <Modal
      title="Install WordPress"
      open={open}
      onCancel={handleClose}
      maskClosable={!submitting && !result}
      width={560}
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
                disabled={availableDomains.length === 0}
              >
                Install WordPress
              </Button>,
            ]
      }
      destroyOnClose
    >
      {!result && (
        <>
          <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
            We&rsquo;ll create a database, download WordPress, run the
            installer, and show the admin password once. The install runs
            in the background — you&rsquo;ll see the row flip from
            &ldquo;installing&rdquo; to &ldquo;ready&rdquo; when it&rsquo;s done
            (usually ~1 minute).
          </Typography.Paragraph>
          {availableDomains.length === 0 && !loadingDomains && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message="No domains available"
              description="Every domain already hosts a WordPress install, or you have no domains yet. Create a new domain first, or delete an existing install."
            />
          )}
          <Form
            form={form}
            layout="vertical"
            disabled={submitting}
            initialValues={{
              admin_username: "admin",
              admin_email: defaultAdminEmail,
              locale: "en_US",
            }}
          >
            <Form.Item
              label="Domain"
              name="domain_id"
              rules={[{ required: true, message: "Pick a domain" }]}
            >
              <Select
                placeholder="Select a domain"
                loading={loadingDomains}
                options={availableDomains.map((d) => ({
                  value: d.id,
                  label: d.name,
                }))}
                showSearch
                optionFilterProp="label"
              />
            </Form.Item>
            <Form.Item
              label="Site title"
              name="site_title"
              rules={[{ required: true, message: "Site title is required" }]}
            >
              <Input placeholder="My WordPress site" autoComplete="off" />
            </Form.Item>
            <Form.Item
              label="Admin username"
              name="admin_username"
              rules={[
                { required: true, message: "Admin username is required" },
                {
                  pattern: /^[a-zA-Z0-9_.-]{3,60}$/,
                  message:
                    "3–60 chars; letters, digits, underscore, dot, dash",
                },
              ]}
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              label="Admin email"
              name="admin_email"
              rules={[
                { required: true, message: "Admin email is required" },
                { type: "email", message: "Must be a valid email" },
              ]}
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              label="Admin password"
              name="admin_password"
              extra="Leave blank to have us generate a strong random password."
            >
              <Input.Password autoComplete="new-password" placeholder="(auto-generated)" />
            </Form.Item>
            <Form.Item label="Locale" name="locale">
              <Select options={LOCALES} showSearch optionFilterProp="label" />
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
            message="WordPress install queued"
            description="The install runs in the background. Copy the password now — it is shown only once. We store only a bcrypt hash."
          />
          <div>
            <Typography.Text strong>Domain</Typography.Text>
            <Input readOnly value={result.domainName} />
          </div>
          <div>
            <Typography.Text strong>Admin username</Typography.Text>
            <Input
              readOnly
              value={result.adminUsername}
              addonAfter={
                <Tooltip title="Copy">
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    onClick={() => copy("Username", result.adminUsername)}
                  />
                </Tooltip>
              }
            />
          </div>
          <div>
            <Typography.Text strong>Admin email</Typography.Text>
            <Input readOnly value={result.adminEmail} />
          </div>
          <div>
            <Typography.Text strong>Admin password</Typography.Text>
            <Input.Password
              readOnly
              value={result.adminPassword}
              visibilityToggle
              addonAfter={
                <Tooltip title="Copy">
                  <Button
                    size="small"
                    type="text"
                    icon={<CopyOutlined />}
                    onClick={() => copy("Password", result.adminPassword)}
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
