import {
  Alert,
  Button,
  Card,
  Form,
  Row,
  Col,
  Select,
  Space,
  Spin,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { apiClient } from "../../../apiClient";
import { getIdentity, type Identity } from "../../../identity";

type Domain = {
  id: string;
  name: string;
  user_id: string;
};

type DomainPHPSettings = {
  php_pool_id?: string | null;
  php_version?: string | null;
  php_memory_limit?: string | null;
  php_upload_max_filesize?: string | null;
  php_post_max_size?: string | null;
  php_max_input_vars?: number | null;
  php_max_execution_time?: number | null;
  php_max_input_time?: number | null;
};

type PHPSettingsFormData = {
  domain_id: string;
  php_memory_limit?: string | null;
  php_upload_max_filesize?: string | null;
  php_post_max_size?: string | null;
  php_max_input_vars?: number | null;
  php_max_execution_time?: number | null;
  php_max_input_time?: number | null;
};

const MEMORY_LIMIT_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "32M", value: "32M" },
  { label: "64M", value: "64M" },
  { label: "128M", value: "128M" },
  { label: "256M", value: "256M" },
  { label: "512M", value: "512M" },
  { label: "1G", value: "1G" },
];

const UPLOAD_MAX_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "1M", value: "1M" },
  { label: "10M", value: "10M" },
  { label: "50M", value: "50M" },
  { label: "100M", value: "100M" },
  { label: "256M", value: "256M" },
  { label: "512M", value: "512M" },
];

const POST_MAX_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "1M", value: "1M" },
  { label: "10M", value: "10M" },
  { label: "50M", value: "50M" },
  { label: "100M", value: "100M" },
  { label: "256M", value: "256M" },
  { label: "512M", value: "512M" },
];

const MAX_INPUT_VARS_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "100", value: 100 },
  { label: "500", value: 500 },
  { label: "1000", value: 1000 },
  { label: "2000", value: 2000 },
  { label: "5000", value: 5000 },
  { label: "10000", value: 10000 },
];

const MAX_EXECUTION_TIME_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "10s", value: 10 },
  { label: "30s", value: 30 },
  { label: "60s", value: 60 },
  { label: "120s", value: 120 },
  { label: "300s", value: 300 },
  { label: "600s", value: 600 },
];

const MAX_INPUT_TIME_OPTIONS = [
  { label: "Use pool default", value: null },
  { label: "10s", value: 10 },
  { label: "30s", value: 30 },
  { label: "60s", value: 60 },
  { label: "120s", value: 120 },
  { label: "300s", value: 300 },
];

export function UserPHPSettingsPage() {
  const [, setMe] = useState<Identity | null>(null);
  const [domains, setDomains] = useState<Domain[]>([]);
  const [selectedDomain, setSelectedDomain] = useState<string | null>(null);
  const [phpSettings, setPhpSettings] = useState<DomainPHPSettings | null>(null);
  const [availableVersions, setAvailableVersions] = useState<string[]>([]);
  const [versionSaving, setVersionSaving] = useState(false);
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form] = Form.useForm<PHPSettingsFormData>();

  // Load identity, domains, and installed PHP versions on mount
  useEffect(() => {
    (async () => {
      const identity = await getIdentity();
      setMe(identity);

      try {
        const resp = await apiClient.get<{ data: Domain[]; total: number }>(
          "/domains",
        );
        setDomains(resp.data?.data ?? []);
      } catch (err) {
        message.error("Failed to load domains");
      }

      try {
        const resp = await apiClient.get<{ versions: string[] }>(
          "/php/versions",
        );
        setAvailableVersions(resp.data?.versions ?? []);
      } catch (err) {
        // Non-fatal: PHP version selector falls back to "Default only".
      }
    })();
  }, []);

  const onChangePHPVersion = async (version: string | null) => {
    if (!selectedDomain) return;
    setVersionSaving(true);
    try {
      if (version === null) {
        await apiClient.delete(`/domains/${selectedDomain}/php-pool`);
      } else {
        await apiClient.post(`/domains/${selectedDomain}/php-pool`, {
          php_version: version,
        });
      }
      message.success(
        version
          ? `Switched to PHP ${version}`
          : "Reverted to server default PHP version",
      );
      const resp = await apiClient.get<DomainPHPSettings>(
        `/domains/${selectedDomain}/php-settings`,
      );
      setPhpSettings(resp.data);
    } catch (err) {
      const e = err as {
        response?: { data?: { error?: string } };
        message?: string;
      };
      message.error(
        e.response?.data?.error ?? e.message ?? "Failed to change PHP version",
      );
    } finally {
      setVersionSaving(false);
    }
  };

  // Load PHP settings when domain is selected
  useEffect(() => {
    if (!selectedDomain) {
      setPhpSettings(null);
      form.resetFields();
      return;
    }

    (async () => {
      setLoading(true);
      try {
        const resp = await apiClient.get<DomainPHPSettings>(
          `/domains/${selectedDomain}/php-settings`,
        );
        setPhpSettings(resp.data);
        form.setFieldsValue({
          domain_id: selectedDomain,
          php_memory_limit: resp.data.php_memory_limit,
          php_upload_max_filesize: resp.data.php_upload_max_filesize,
          php_post_max_size: resp.data.php_post_max_size,
          php_max_input_vars: resp.data.php_max_input_vars,
          php_max_execution_time: resp.data.php_max_execution_time,
          php_max_input_time: resp.data.php_max_input_time,
        });
      } catch (err) {
        message.error("Failed to load PHP settings");
      } finally {
        setLoading(false);
      }
    })();
  }, [selectedDomain, form]);

  const onSave = async (values: PHPSettingsFormData) => {
    if (!selectedDomain) return;

    setSubmitting(true);
    try {
      await apiClient.patch(`/domains/${selectedDomain}/php-settings`, {
        php_memory_limit: values.php_memory_limit,
        php_upload_max_filesize: values.php_upload_max_filesize,
        php_post_max_size: values.php_post_max_size,
        php_max_input_vars: values.php_max_input_vars,
        php_max_execution_time: values.php_max_execution_time,
        php_max_input_time: values.php_max_input_time,
      });
      message.success("PHP settings updated successfully");
      // Reload settings to confirm
      if (selectedDomain) {
        const resp = await apiClient.get<DomainPHPSettings>(
          `/domains/${selectedDomain}/php-settings`,
        );
        setPhpSettings(resp.data);
      }
    } catch (err) {
      message.error("Failed to update PHP settings");
    } finally {
      setSubmitting(false);
    }
  };

  // Fields the Save button cares about. AntD's form state changes don't
  // trigger parent re-renders, so we can't compute `hasChanges` inline —
  // we have to evaluate it inside a Form.Item shouldUpdate wrapper so it
  // re-runs on every form mutation. Typed literal-tuple so
  // form.isFieldsTouched's keyof-narrowed overload accepts it.
  const dirtyFields: (keyof PHPSettingsFormData)[] = [
    "php_memory_limit",
    "php_upload_max_filesize",
    "php_post_max_size",
    "php_max_input_vars",
    "php_max_execution_time",
    "php_max_input_time",
  ];

  return (
    <div style={{ padding: 24, maxWidth: 800, margin: "0 auto" }}>
      <Space orientation="vertical" size="large" style={{ width: "100%" }}>
        <Typography.Title level={2} style={{ margin: 0 }}>
          PHP Settings
        </Typography.Title>

        <Alert
          title="Caution"
          description="Changing PHP settings can affect your website performance and functionality. Incorrect values may cause errors or prevent your site from functioning properly. Changes apply after the next request to PHP."
          type="warning"
          showIcon
        />

        <Card>
          <Form<PHPSettingsFormData>
            form={form}
            layout="vertical"
            onFinish={onSave}
          >
            <Form.Item
              label="Domain"
              name="domain_id"
              rules={[{ required: true, message: "Please select a domain" }]}
            >
              <Select
                placeholder="Select a domain"
                onChange={setSelectedDomain}
                options={domains.map((d) => ({
                  label: d.name,
                  value: d.id,
                }))}
              />
            </Form.Item>

            <Spin spinning={loading}>
              {selectedDomain && phpSettings && (
                <>
                  <Form.Item
                    label={
                      phpSettings.php_version
                        ? `PHP Version (${phpSettings.php_version})`
                        : "PHP Version"
                    }
                  >
                    <Select
                      value={phpSettings.php_version ?? null}
                      loading={versionSaving}
                      disabled={versionSaving}
                      onChange={(v) => onChangePHPVersion(v)}
                      options={[
                        { label: "Server default", value: null },
                        ...availableVersions.map((v) => ({
                          label: `PHP ${v}`,
                          value: v,
                        })),
                      ]}
                    />
                  </Form.Item>

                  <Typography.Title level={5}>Resource Limits</Typography.Title>
                  <Row gutter={[16, 16]}>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="Memory Limit"
                        name="php_memory_limit"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={MEMORY_LIMIT_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="Upload Max File Size"
                        name="php_upload_max_filesize"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={UPLOAD_MAX_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="POST Max Size"
                        name="php_post_max_size"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={POST_MAX_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="Max Input Variables"
                        name="php_max_input_vars"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={MAX_INPUT_VARS_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                  </Row>

                  <Typography.Title level={5}>Execution Limits</Typography.Title>
                  <Row gutter={[16, 16]}>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="Max Execution Time"
                        name="php_max_execution_time"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={MAX_EXECUTION_TIME_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} sm={12}>
                      <Form.Item
                        label="Max Input Time"
                        name="php_max_input_time"
                      >
                        <Select
                          placeholder="Use pool default"
                          allowClear
                          options={MAX_INPUT_TIME_OPTIONS}
                        />
                      </Form.Item>
                    </Col>
                  </Row>

                  <Form.Item
                    noStyle
                    shouldUpdate={(prev, cur) =>
                      dirtyFields.some((f) => prev[f] !== cur[f])
                    }
                  >
                    {() => {
                      const hasChanges =
                        !!selectedDomain &&
                        form.isFieldsTouched(dirtyFields);
                      return (
                        <Form.Item
                          style={{ marginBottom: 0, marginTop: 24 }}
                        >
                          <Button
                            type="primary"
                            htmlType="submit"
                            loading={submitting}
                            disabled={!hasChanges}
                          >
                            Save Changes
                          </Button>
                        </Form.Item>
                      );
                    }}
                  </Form.Item>
                </>
              )}
            </Spin>
          </Form>
        </Card>
      </Space>
    </div>
  );
}
