// Install modal — provisions an application (WordPress today; future
// apps land via the M19 registry). POSTs the generic shape to
// /applications: {app_type, domain_id, subdirectory, use_www, params}.
// The backend looks up the descriptor, validates `params` against its
// InstallParamSchema, provisions a DB if descriptor.RequiresDB, and
// dispatches the agent install via app.install with the matching
// app_type. Returns 202 with the admin password delivered exactly
// once — we show it in a reveal-once panel.

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
import { CopyOutlined, CheckCircleTwoTone, AppstoreOutlined } from "@ant-design/icons";
import { useInvalidate } from "@refinedev/core";
import { apiClient } from "../../../apiClient";

type Domain = { id: string; name: string };

// AppDescriptor mirrors the JSON the server's GET /applications/registry
// returns. We carry only the fields the UI renders today; new fields can
// be added without breaking older bundles because Select reads keys it
// knows.
type AppDescriptor = {
  name: string;
  display_name: string;
  description?: string;
  default_subdirectory: string;
  requires_db: boolean;
};

type Props = {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
  defaultAdminEmail?: string;
};

type CreatedResult = {
  appType: string;
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

export const InstallApplicationModal = ({
  open,
  onClose,
  onSuccess,
  defaultAdminEmail,
}: Props) => {
  const [form] = Form.useForm<{
    app_type: string;
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
  const [apps, setApps] = useState<AppDescriptor[]>([]);
  const [loadingApps, setLoadingApps] = useState(false);
  const [result, setResult] = useState<CreatedResult | null>(null);
  const invalidate = useInvalidate();

  const selectedAppType = Form.useWatch("app_type", form);
  const selectedDomainId = Form.useWatch("domain_id", form);
  const domainSelected = !!selectedDomainId;
  const selectedApp = apps.find((a) => a.name === selectedAppType);

  const refreshLists = () => {
    invalidate({ resource: "applications", invalidates: ["list"] });
    invalidate({ resource: "databases", invalidates: ["list"] });
    invalidate({ resource: "database-users", invalidates: ["list"] });
    onSuccess();
  };

  // Load registry + domains when the modal opens. The picker defaults
  // to "wordpress" so the form looks identical to today; later steps
  // light up DokuWiki / MediaWiki via additional registry entries.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    setLoadingDomains(true);
    setLoadingApps(true);
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
    apiClient
      .get<{ data: AppDescriptor[] }>("/applications/registry")
      .then((resp) => {
        if (!alive) return;
        const list = resp.data?.data ?? [];
        setApps(list);
        // Default to WordPress when present so the form doesn't change
        // shape between releases that add new app types at the front.
        const wp = list.find((a) => a.name === "wordpress");
        const defaultName = wp?.name ?? list[0]?.name ?? "wordpress";
        form.setFieldsValue({ app_type: defaultName });
      })
      .catch((err) => {
        if (!alive) return;
        message.error(extractError(err, "Failed to load app catalog"));
        // Fallback: surface a WordPress-only catalog so the form still
        // renders if the server hasn't redeployed with /applications/registry.
        setApps([
          {
            name: "wordpress",
            display_name: "WordPress",
            default_subdirectory: "",
            requires_db: true,
          },
        ]);
        form.setFieldsValue({ app_type: "wordpress" });
      })
      .finally(() => {
        if (alive) setLoadingApps(false);
      });
    return () => {
      alive = false;
    };
  }, [open, form]);

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
      // M19 generic create. The per-app fields (site_title, admin_*)
      // live under `params`; the descriptor's InstallParamSchema on the
      // server validates them. Only WordPress is in the picker today,
      // so the body shape matches what the legacy /wordpress-installs
      // route accepted just rewrapped.
      const params: Record<string, unknown> = {
        admin_username: vals.admin_username,
        admin_email: vals.admin_email,
        site_title: vals.site_title,
        locale: vals.locale || "en_US",
      };
      if (vals.admin_password) {
        params.admin_password = vals.admin_password;
      }
      const resp = await apiClient.post<{
        id: string;
        app_type: string;
        domain_id: string;
        db_id: string;
        admin_username: string;
        admin_email: string;
        admin_password: string;
      }>("/applications", {
        app_type: vals.app_type,
        domain_id: vals.domain_id,
        use_www: vals.use_www || false,
        subdirectory: vals.subdirectory || "",
        params,
      });
      const domainName =
        domains.find((d) => d.id === vals.domain_id)?.name ?? vals.domain_id;
      setResult({
        appType: resp.data.app_type,
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
        form.setFields([
          {
            name: "subdirectory",
            errors: [
              "This domain already hosts the same application at that location — pick a different subdirectory",
            ],
          },
        ]);
      } else {
        message.error(extractError(err, "Failed to install application"));
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

  const validateSubdirectory = (_: unknown, value: string) => {
    if (!value) {
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

  const availableDomains = domains;

  return (
    <Modal
      title="Install application"
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
                disabled={availableDomains.length === 0 || apps.length === 0}
              >
                Install {selectedApp?.display_name ?? "application"}
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
                We&rsquo;ll provision the application&rsquo;s database (if
                it needs one), download the app, run its installer, and
                show the admin password once. The install runs in the
                background — the row flips from
                &ldquo;installing&rdquo; to &ldquo;ready&rdquo; when
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
              app_type: "wordpress",
              use_www: false,
              subdirectory: "",
              admin_username: "admin",
              admin_email: defaultAdminEmail,
              locale: "en_US",
            }}
          >
            <Form.Item
              label="Application"
              name="app_type"
              rules={[{ required: true, message: "Pick an application" }]}
            >
              <Select
                placeholder="Select an application"
                loading={loadingApps}
                suffixIcon={<AppstoreOutlined />}
                options={apps.map((a) => ({
                  value: a.name,
                  label: a.display_name,
                }))}
                showSearch
                optionFilterProp="label"
              />
            </Form.Item>

            {selectedApp?.description && (
              <Form.Item style={{ marginTop: -8, marginBottom: 16 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {selectedApp.description}
                </Typography.Text>
              </Form.Item>
            )}

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
                  <Input placeholder="My site" autoComplete="off" />
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
            message="Install queued"
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
