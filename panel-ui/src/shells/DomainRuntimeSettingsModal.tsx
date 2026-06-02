import { useEffect, useState } from "react";
import {
  Modal,
  Tabs,
  Form,
  Select,
  Input,
  Button,
  Space,
  Tag,
  Typography,
  Divider,
  Alert,
  Spin,
  notification,
  Card,
} from "antd";
import {
  ApiOutlined,
  CodeOutlined,
  ReloadOutlined,
  PlusOutlined,
  DeleteOutlined,
  CopyOutlined,
  CheckCircleOutlined,
  SyncOutlined,
  CloseCircleOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { apiClient } from "../apiClient";
import { useAuth } from "../auth/AuthContext";

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  runtime_type?: string;
};

interface RuntimeService {
  id: string;
  domain_id: string;
  runtime: string;
  entry_point: string;
  listen_port: number;
  env_vars: Record<string, string>;
  status: string;
  last_error?: string;
  systemd_unit: string;
}

interface RuntimeResponse {
  runtime_service: RuntimeService;
  logs: string;
  agent_status?: Record<string, unknown>;
}

interface DomainRuntimeSettingsModalProps {
  domain: Domain;
  open: boolean;
  onClose: () => void;
  onSuccess?: () => void;
}

export const DomainRuntimeSettingsModal = ({
  domain,
  open,
  onClose,
  onSuccess,
}: DomainRuntimeSettingsModalProps) => {
  const [loading, setLoading] = useState(false);
  const { isAdmin } = useAuth();
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [activeTab, setActiveTab] = useState("config");
  const [runtimeType, setRuntimeType] = useState<string>(domain.runtime_type || "php");
  const [runtimeService, setRuntimeService] = useState<RuntimeService | null>(null);
  const [logs, setLogs] = useState<string>("");
  const [envVars, setEnvVars] = useState<{ key: string; value: string }[]>([]);
  const [form] = Form.useForm();

  // Load runtime service details and logs
  const loadData = async (showLoading = true) => {
    if (showLoading) setLoading(true);
    try {
      // First ensure runtime_type is synchronized
      setRuntimeType(domain.runtime_type || "php");
      
      const needsProxy = ["nodejs", "python", "go", "docker"].includes(domain.runtime_type || "php");
      if (needsProxy) {
        const res = await apiClient.get<RuntimeResponse>(`/domains/${domain.id}/runtime`);
        setRuntimeService(res.data.runtime_service);
        setLogs(res.data.logs || "No logs available.");
        
        // Populate env vars list
        const vars = res.data.runtime_service.env_vars || {};
        setEnvVars(Object.entries(vars).map(([key, value]) => ({ key, value })));
        
        form.setFieldsValue({
          entry_point: res.data.runtime_service.entry_point || "",
        });
      } else {
        setRuntimeService(null);
        setLogs("");
        setEnvVars([]);
      }
    } catch (err) {
      notification.error({
        message: "Failed to load runtime details",
        description: (err as Error).message,
      });
    } finally {
      if (showLoading) setLoading(false);
    }
  };

  useEffect(() => {
    if (open) {
      loadData(true);
    }
  }, [open, domain.runtime_type]);

  // Handle runtime type change
  const handleTypeChange = async (value: string) => {
    setSaving(true);
    try {
      await apiClient.patch(`/domains/${domain.id}`, { runtime_type: value });
      notification.success({
        message: "Runtime Type Updated",
        description: `Successfully switched to ${value.toUpperCase()} runtime. Reconciler is converging the backend.`,
      });
      // Invalidate query and reload
      if (onSuccess) onSuccess();

      // Reflect the new runtime locally and reload runtime details.
      // (Don't mutate the `domain` prop directly — React won't see it
      // and a parent re-render would clobber it.)
      setRuntimeType(value);
      await loadData(true);
    } catch (err) {
      notification.error({
        message: "Failed to change runtime type",
        description: (err as Error).message,
      });
    } finally {
      setSaving(false);
    }
  };

  // Add environment variable row
  const addEnvVar = () => {
    setEnvVars([...envVars, { key: "", value: "" }]);
  };

  // Delete environment variable row
  const removeEnvVar = (index: number) => {
    const list = [...envVars];
    list.splice(index, 1);
    setEnvVars(list);
  };

  // Update environment variable row value
  const updateEnvVar = (index: number, field: "key" | "value", val: string) => {
    const list = [...envVars];
    list[index][field] = val;
    setEnvVars(list);
  };

  // Save entry point and env vars
  const handleSaveConfig = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);

      // Validate env vars keys
      const parsedEnv: Record<string, string> = {};
      for (const item of envVars) {
        const trimmedKey = item.key.trim();
        if (!trimmedKey) continue;
        if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(trimmedKey)) {
          notification.error({
            message: "Invalid Env Var Key",
            description: `Key "${trimmedKey}" is invalid. Keys must be alphanumeric and start with a letter/underscore.`,
          });
          setSaving(false);
          return;
        }
        parsedEnv[trimmedKey] = item.value;
      }

      await apiClient.patch(`/domains/${domain.id}/runtime`, {
        entry_point: values.entry_point || "",
        env_vars: parsedEnv,
      });

      notification.success({
        message: "Configuration Saved",
        description: "Application configuration updated. The service is being restarted with new environment variables.",
      });

      await loadData(false);
    } catch (err) {
      notification.error({
        message: "Failed to save configuration",
        description: (err as Error).message,
      });
    } finally {
      setSaving(false);
    }
  };

  // Restart Service
  const handleRestart = async () => {
    setRestarting(true);
    try {
      await apiClient.post(`/domains/${domain.id}/runtime/restart`);
      notification.success({
        message: "Restart Command Dispatched",
        description: "Systemd service restart initiated.",
      });
      // Small delay then reload
      setTimeout(() => loadData(false), 2000);
    } catch (err) {
      notification.error({
        message: "Failed to restart service",
        description: (err as Error).message,
      });
    } finally {
      setRestarting(false);
    }
  };

  // Copy Logs
  const handleCopyLogs = () => {
    navigator.clipboard.writeText(logs);
    notification.success({ message: "Logs copied to clipboard!" });
  };

  const getStatusTag = (status: string) => {
    switch (status) {
      case "active":
      case "running":
        return (
          <Tag color="success" icon={<CheckCircleOutlined />}>
            Running
          </Tag>
        );
      case "pending":
      case "deploying":
        return (
          <Tag color="processing" icon={<SyncOutlined spin />}>
            Deploying
          </Tag>
        );
      case "failed":
        return (
          <Tag color="error" icon={<CloseCircleOutlined />}>
            Failed
          </Tag>
        );
      default:
        return <Tag color="warning">{status.toUpperCase()}</Tag>;
    }
  };

  const isProxyRuntime = ["nodejs", "python", "go", "docker"].includes(runtimeType);

  return (
    <Modal
      title={
        <div style={{ display: "flex", alignItems: "center", gap: 12, paddingBottom: 8 }}>
          <ApiOutlined style={{ fontSize: 24, color: "#1890ff" }} />
          <div>
            <Typography.Text strong style={{ fontSize: 18 }}>
              Runtime & Environment
            </Typography.Text>
            <div style={{ fontSize: 12, fontWeight: "normal", color: "#8c8c8c" }}>
              Domain: <Typography.Text code>{domain.name}</Typography.Text>
            </div>
          </div>
        </div>
      }
      open={open}
      onCancel={onClose}
      width={850}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
      styles={{ body: { padding: "8px 24px 24px 24px" } }}
    >
      <Tabs activeKey={activeTab} onChange={setActiveTab} style={{ minHeight: 400 }}>
        {/* TAB 1: CONFIGURATION */}
        <Tabs.TabPane
          tab={
            <span>
              <CodeOutlined />
              Configuration
            </span>
          }
          key="config"
        >
          <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
            {/* Runtime strategy picker */}
            <Card
              style={{
                borderRadius: 8,
                background: "linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%)",
                border: "none",
              }}
            >
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  flexWrap: "wrap",
                  gap: 12,
                }}
              >
                <div>
                  <Typography.Title level={5} style={{ margin: 0 }}>
                    Runtime Hosting Engine
                  </Typography.Title>
                  <Typography.Text type="secondary">
                    Choose the backend strategy for serving web traffic.
                  </Typography.Text>
                </div>
                <Select
                  value={runtimeType}
                  onChange={handleTypeChange}
                  style={{ width: 220 }}
                  loading={saving}
                >
                  <Select.Option value="php">PHP-FPM (Fully Managed)</Select.Option>
                  <Select.Option value="nodejs">Node.js (Reverse Proxy)</Select.Option>
                  <Select.Option value="python">Python (WSGI/ASGI Proxy)</Select.Option>
                  <Select.Option value="go">Go Binary (Reverse Proxy)</Select.Option>
                  {/* Docker runs as a user-level docker invocation, which on a
                      non-rootless host is root-equivalent. The API rejects it
                      for non-admins (docker_runtime_admin_only); hide the option
                      here so the choice matches what the server will accept. */}
                  {(isAdmin || runtimeType === "docker") && (
                    <Select.Option value="docker">Docker Container (Isolated)</Select.Option>
                  )}
                  <Select.Option value="static">Static Files Only</Select.Option>
                </Select>
              </div>
            </Card>

            {loading ? (
              <div style={{ textAlign: "center", padding: "40px 0" }}>
                <Spin size="large" tip="Loading runtime parameters..." />
              </div>
            ) : (
              <>
                {/* Standard PHP-FPM detail */}
                {runtimeType === "php" && (
                  <Alert
                    message="Fully Managed PHP Engine"
                    description="This domain is processed natively by Jabali's optimized PHP-FPM pools. You can customize php.ini overrides in the Domain Nginx Settings modal."
                    type="info"
                    showIcon
                  />
                )}

                {/* Static only detail */}
                {runtimeType === "static" && (
                  <Alert
                    message="Static Files Only"
                    description="Nginx will serve HTML, CSS, JS, and media assets directly from your docroot directory without forwarding traffic to any backend process."
                    type="info"
                    showIcon
                  />
                )}

                {/* Custom Process Runtimes (Node, Python, Go, Docker) */}
                {isProxyRuntime && (
                  <>
                    {/* Live State dashboard card */}
                    {runtimeService && (
                      <div
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          alignItems: "center",
                          background: "#fafafa",
                          padding: "12px 18px",
                          borderRadius: 8,
                          border: "1px solid #f0f0f0",
                        }}
                      >
                        <Space size="large">
                          <div>
                            <Typography.Text type="secondary" style={{ fontSize: 11, display: "block" }}>
                              PORT ASSIGNMENT
                            </Typography.Text>
                            <Typography.Text strong style={{ fontSize: 16 }}>
                              {runtimeService.listen_port}
                            </Typography.Text>
                          </div>
                          <Divider type="vertical" style={{ height: 32 }} />
                          <div>
                            <Typography.Text type="secondary" style={{ fontSize: 11, display: "block" }}>
                              SERVICE STATUS
                            </Typography.Text>
                            <div style={{ marginTop: 4 }}>{getStatusTag(runtimeService.status)}</div>
                          </div>
                        </Space>

                        <Space>
                          <Button
                            type="dashed"
                            icon={<ReloadOutlined spin={restarting} />}
                            onClick={handleRestart}
                            loading={restarting}
                          >
                            Restart Service
                          </Button>
                        </Space>
                      </div>
                    )}

                    {runtimeService?.last_error && (
                      <Alert
                        message="Application Crash Detected"
                        description={runtimeService.last_error}
                        type="error"
                        showIcon
                      />
                    )}

                    <Form form={form} layout="vertical">
                      <Form.Item
                        name="entry_point"
                        label={
                          <Typography.Text strong>
                            Application Entrypoint / Executable Command
                          </Typography.Text>
                        }
                        tooltip="Specify the file or script that starts your app (e.g. index.js, app.py, main.go, or docker build context path)"
                      >
                        <Input
                          prefix={<CodeOutlined />}
                          placeholder={
                            runtimeType === "nodejs"
                              ? "index.js (defaults to main/index.js)"
                              : runtimeType === "python"
                                ? "app.py (defaults to app.py)"
                                : runtimeType === "go"
                                  ? "server (compiled binary path)"
                                  : "Dockerfile path / image name"
                          }
                        />
                      </Form.Item>
                    </Form>

                    <Divider style={{ margin: "12px 0" }} />

                    {/* Environment Variables Header */}
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                      <div>
                        <Typography.Text strong style={{ fontSize: 15 }}>
                          Environment Variables
                        </Typography.Text>
                        <div style={{ fontSize: 12, color: "#8c8c8c" }}>
                          Inject configuration variables securely into your runtime process.
                        </div>
                      </div>
                      <Button type="primary" size="small" icon={<PlusOutlined />} onClick={addEnvVar}>
                        Add Variable
                      </Button>
                    </div>

                    {/* Env Vars rows list */}
                    <div style={{ maxHeight: 250, overflowY: "auto", paddingRight: 4 }}>
                      {envVars.length === 0 ? (
                        <div
                          style={{
                            textAlign: "center",
                            padding: "24px",
                            background: "#fafafa",
                            borderRadius: 6,
                            border: "1px dashed #d9d9d9",
                            marginTop: 8,
                          }}
                        >
                          <Typography.Text type="secondary">
                            No custom environment variables injected yet. Click "Add Variable" to define some.
                          </Typography.Text>
                        </div>
                      ) : (
                        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
                          {envVars.map((item, index) => (
                            <div key={index} style={{ display: "flex", gap: 8, alignItems: "center" }}>
                              <Input
                                placeholder="VARIABLE_NAME"
                                value={item.key}
                                onChange={(e) => updateEnvVar(index, "key", e.target.value)}
                                style={{ flex: 1, fontFamily: "monospace" }}
                              />
                              <Typography.Text type="secondary">=</Typography.Text>
                              <Input.Password
                                placeholder="value"
                                value={item.value}
                                onChange={(e) => updateEnvVar(index, "value", e.target.value)}
                                style={{ flex: 1.5 }}
                              />
                              <Button
                                type="text"
                                danger
                                icon={<DeleteOutlined />}
                                onClick={() => removeEnvVar(index)}
                              />
                            </div>
                          ))}
                        </div>
                      )}
                    </div>

                    <div style={{ marginTop: 24, textAlign: "right" }}>
                      <Button
                        type="primary"
                        icon={<ThunderboltOutlined />}
                        loading={saving}
                        onClick={handleSaveConfig}
                      >
                        Apply Config & Deploy
                      </Button>
                    </div>
                  </>
                )}
              </>
            )}
          </div>
        </Tabs.TabPane>

        {/* TAB 2: TERMINAL LOGS */}
        {isProxyRuntime && (
          <Tabs.TabPane
            tab={
              <span>
                <CodeOutlined />
                Execution Logs
              </span>
            }
            key="logs"
          >
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <div>
                  <Typography.Text strong>Systemd Journal Logs</Typography.Text>
                  <div style={{ fontSize: 12, color: "#8c8c8c" }}>
                    Real-time tail of stdout & stderr logs from journalctl.
                  </div>
                </div>
                <Space>
                  <Button size="small" icon={<SyncOutlined spin={loading} />} onClick={() => loadData(false)}>
                    Refresh
                  </Button>
                  <Button size="small" icon={<CopyOutlined />} onClick={handleCopyLogs}>
                    Copy Logs
                  </Button>
                </Space>
              </div>

              <Card
                styles={{
                  body: {
                    padding: 12,
                    background: "#1e1e1e",
                    borderRadius: 6,
                  },
                }}
              >
                <div
                  style={{
                    height: 380,
                    overflowY: "auto",
                    fontFamily: "monospace",
                    fontSize: 12,
                    color: "#d4d4d4",
                    whiteSpace: "pre-wrap",
                    lineHeight: "1.6",
                  }}
                >
                  {loading && logs === "" ? (
                    <div style={{ textAlign: "center", paddingTop: 100 }}>
                      <Spin size="small" tip="Fetching logs..." />
                    </div>
                  ) : (
                    logs
                  )}
                </div>
              </Card>
            </div>
          </Tabs.TabPane>
        )}
      </Tabs>
    </Modal>
  );
};
