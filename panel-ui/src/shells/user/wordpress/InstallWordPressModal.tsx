// Install modal — provisions a WordPress site on a domain. POSTs to
// /wordpress-installs; backend creates the DB + DB-user + grant, spawns
// an async agent job, and returns 202 with the admin password delivered
// exactly once. We show it in a reveal-once panel using the same pattern
// as Quick Setup.
//
// Multi-install per domain: a domain can host multiple WordPress installs
// as long as each lives at a distinct subdirectory ("" = docroot install).
// The dropdown therefore lists every domain the user owns; the backend
// returns 409 install_exists only if (domain, subdirectory) is already
// taken — surfaced as a field error on the subdirectory input.

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
  Switch,
} from "antd";
import { CopyOutlined, CheckCircleTwoTone } from "@ant-design/icons";
import { useInvalidate } from "@refinedev/core";
import { apiClient } from "../../../apiClient";

type Domain = { id: string; name: string };

type Props = {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
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

// Subdirectory validation: must start with lowercase letter or digit,
// 1-64 chars total, only lowercase letters, digits, underscore, dash.
// Matches server-side regex: ^[a-z0-9][a-z0-9_-]{0,63}$
const SUBDIRECTORY_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const RESERVED_SUBDIRS = new Set(["wp-admin", "wp-includes", "wp-content"]);

export const InstallWordPressModal = ({
  open,
  onClose,
  onSuccess,
  defaultAdminEmail,
}: Props) => {
  const [form] = Form.useForm<{
    domain_id: string;
    use_www: boolean;
    subdirectory: string;
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

  // Watch domain_id to show/hide Step 2 fields
  const selectedDomainId = Form.useWatch("domain_id", form);
  const domainSelected = !!selectedDomainId;

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
        use_www: vals.use_www || false,
        subdirectory: vals.subdirectory || undefined,
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
      const e = err as ApiError;
      if (e.response?.data?.error === "invalid_subdirectory") {
        const detail = e.response.data.detail ?? "Invalid subdirectory";
        form.setFields([{ name: "subdirectory", errors: [detail] }]);
      } else if (e.response?.data?.error === "install_exists") {
        // Server rejected because (domain, subdirectory) is already taken.
        // Surface as a subdirectory field error so the user can pick a
        // different subdir without losing the rest of the form.
        form.setFields([
          {
            name: "subdirectory",
            errors: [
              "This domain already hosts a WordPress install at that location — pick a different subdirectory",
            ],
          },
        ]);
      } else {
        message.error(extractError(err, "Failed to install WordPress"));
      }
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

  const validateSubdirectory = (_: any, value: string) => {
    if (!value) {
      // subdirectory is optional
      return Promise.resolve();
    }
    if (!SUBDIRECTORY_PATTERN.test(value)) {
      return Promise.reject(
        new Error("Must start with letter/digit; 1–64 chars, letters, digits, underscore, dash")
      );
    }
    if (RESERVED_SUBDIRS.has(value.toLowerCase())) {
      return Promise.reject(
        new Error(`"${value}" is reserved; use a different subdirectory`)
      );
    }
    return Promise.resolve();
  };

  // Show every domain. The backend enforces uniqueness at
  // (domain, subdirectory) granularity, not per-domain — so a domain that
  // already hosts /blog can still receive a docroot install or a /shop
  // install. install_exists is surfaced as a subdirectory field error in
  // handleSubmit when the (domain, subdir) slot is genuinely taken.
  const availableDomains = domains;

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
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message="What happens next"
            description={
              <>
                We&rsquo;ll create a database, download WordPress, run the
                installer, and show the admin password once. The install
                runs in the background — you&rsquo;ll see the row flip
                from &ldquo;installing&rdquo; to &ldquo;ready&rdquo; when
                it&rsquo;s done (usually ~1 minute).
              </>
            }
          />
          {availableDomains.length === 0 && !loadingDomains && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message="No domains yet"
              description="You have no domains. Create one first."
            />
          )}
          <Form
            form={form}
            layout="vertical"
            disabled={submitting}
            initialValues={{
              use_www: false,
              subdirectory: "",
              admin_username: "admin",
              admin_email: defaultAdminEmail,
              locale: "en_US",
            }}
          >
            {/* Step 1: Domain selector only */}
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

            {/* Step 2: Remaining fields (revealed after domain selection) */}
            {domainSelected && (
              <>
                <Form.Item
                  label="Use www prefix"
                  name="use_www"
                  valuePropName="checked"
                >
                  <Switch />
                </Form.Item>
                <Form.Item style={{ marginBottom: 16 }}>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    Install on www.domain.com instead of domain.com
                  </Typography.Text>
                </Form.Item>

                <Form.Item
                  label="Directory (optional)"
                  name="subdirectory"
                  rules={[{ validator: validateSubdirectory }]}
                >
                  <Input
                    placeholder="Leave empty to install in root"
                    autoComplete="off"
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
              </>
            )}
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
