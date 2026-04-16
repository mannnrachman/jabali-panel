// Shared settings modal for nginx custom directives used by both admin and user domain lists.
// Opens a modal with tabs: "Rule Builder" (placeholder) and "Raw Directives" (functional textarea).
// The Raw Directives tab allows users to edit nginx configuration safely.
import { useState } from "react";
import {
  SettingOutlined,
  ToolOutlined,
  CodeOutlined,
  CheckOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import {
  Button,
  Modal,
  Alert,
  Tabs,
  Input,
  Space,
  Typography,
} from "antd";
import { useInvalidate, useNotification } from "@refinedev/core";

import { apiClient } from "../apiClient";

// Minimal shape — admin and user shells have slightly different Domain
// records but this button only cares about these fields.
export type DomainSettingsTarget = {
  id: string;
  name: string;
  nginx_custom_directives?: string | null;
};

// Placeholder component for the Rule Builder tab
const ComingSoonPlaceholder = () => (
  <div
    style={{
      display: "flex",
      flexDirection: "column",
      alignItems: "center",
      justifyContent: "center",
      padding: "60px 24px",
      textAlign: "center",
    }}
  >
    <ToolOutlined
      style={{
        fontSize: 48,
        color: "#bfbfbf",
        marginBottom: 16,
      }}
    />
    <Typography.Title level={4} style={{ color: "#595959" }}>
      Rule Builder
    </Typography.Title>
    <Typography.Text type="secondary">
      Coming in next release. For now, use Raw Directives.
    </Typography.Text>
  </div>
);

// Raw Directives editor component
const RawDirectivesEditor = ({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) => (
  <div>
    <div style={{ marginBottom: 12 }}>
      <Typography.Text strong>Raw directives</Typography.Text>
    </div>
    <Input.TextArea
      rows={14}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={`# Example:
rewrite ^/old$ /new permanent;
add_header X-Frame-Options "DENY" always;`}
      style={{
        fontFamily: "monospace",
        fontSize: 12,
      }}
    />
    <Typography.Text
      type="secondary"
      style={{ display: "block", marginTop: 8, fontSize: 12 }}
    >
      Restricted to safe directives (rewrite, add_header, proxy_pass, etc.).
      Dangerous directives are blocked.
    </Typography.Text>
  </div>
);

export const DomainSettingsButton = ({
  domain,
}: {
  domain: DomainSettingsTarget;
}) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [directivesValue, setDirectivesValue] = useState(
    domain.nginx_custom_directives ?? ""
  );
  const [isSaving, setIsSaving] = useState(false);
  const invalidate = useInvalidate();
  const { open } = useNotification();

  const handleOpenModal = () => {
    // Re-sync from prop in case the value was updated elsewhere
    setDirectivesValue(domain.nginx_custom_directives ?? "");
    setIsModalOpen(true);
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
  };

  const handleApply = async () => {
    setIsSaving(true);
    try {
      await apiClient.patch(`/domains/${domain.id}`, {
        nginx_custom_directives: directivesValue,
      });
      open?.({
        type: "success",
        message: "Directives saved",
      });
      invalidate({ resource: "domains", invalidates: ["list"] });
      setIsModalOpen(false);
    } catch (err) {
      open?.({
        type: "error",
        message: "Failed to save",
        description: (err as Error).message,
      });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <>
      <Button
        type="text"
        size="small"
        icon={<SettingOutlined />}
        onClick={handleOpenModal}
      >
        Settings
      </Button>

      <Modal
        title={`Nginx Directives for ${domain.name}`}
        open={isModalOpen}
        onCancel={handleCloseModal}
        width={720}
        footer={[
          <Button key="cancel" onClick={handleCloseModal}>
            Cancel
          </Button>,
          <Button
            key="apply"
            type="primary"
            icon={<CheckOutlined />}
            onClick={handleApply}
            loading={isSaving}
          >
            Apply
          </Button>,
        ]}
      >
        <Alert
          type="warning"
          icon={<WarningOutlined />}
          message="Use with caution"
          description="Incorrect directives can break your website. Changes are tested with nginx before applying, but you are responsible for ensuring your configuration is correct."
          showIcon
          style={{ marginBottom: 24 }}
        />

        <Tabs
          defaultActiveKey="raw"
          items={[
            {
              key: "builder",
              label: (
                <span>
                  <ToolOutlined /> Rule Builder
                </span>
              ),
              children: <ComingSoonPlaceholder />,
            },
            {
              key: "raw",
              label: (
                <span>
                  <CodeOutlined /> Raw Directives
                </span>
              ),
              children: (
                <RawDirectivesEditor
                  value={directivesValue}
                  onChange={setDirectivesValue}
                />
              ),
            },
          ]}
        />
      </Modal>
    </>
  );
};
