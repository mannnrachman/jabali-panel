import { useState } from "react";
import { Button, Card, Space, Spin, Typography, message } from "antd";
import { FolderOutlined } from "@ant-design/icons";
import { ssoFileBrowser } from "../../../apiClient";

export const UserFilesLauncher = () => {
  const [loading, setLoading] = useState(false);

  const handleOpenFileBrowser = async () => {
    // Open a blank tab synchronously so it counts as a user-initiated
    // popup; most browsers block window.open() that fires after an
    // await. We navigate the tab once the SSO redirect URL resolves.
    // The tab is opened with `noopener,noreferrer` so filebrowser can't
    // reach back through window.opener.
    const tab = window.open("", "_blank", "noopener,noreferrer");
    try {
      setLoading(true);
      const response = await ssoFileBrowser();
      if (tab) {
        tab.location.href = response.redirect_url;
      } else {
        // Pop-up blocker closed the tab before we got the URL; fall
        // back to same-tab navigation so the user isn't stranded.
        window.location.assign(response.redirect_url);
      }
    } catch (error) {
      if (tab) tab.close();
      const errorMsg =
        error instanceof Error ? error.message : "Could not open File Manager";
      message.error(`Could not open File Manager: ${errorMsg}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{ padding: 24 }}>
      <Card
        style={{
          maxWidth: 600,
          margin: "0 auto",
          marginTop: 60,
          textAlign: "center",
        }}
      >
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <div>
            <FolderOutlined
              style={{ fontSize: 48, color: "#1890ff", marginBottom: 16 }}
            />
          </div>

          <Typography.Title level={3} style={{ margin: 0 }}>
            My Files
          </Typography.Title>

          <Typography.Paragraph style={{ color: "#666", marginBottom: 24 }}>
            Your files are managed via the dedicated file manager. Click the
            button below to open it.
          </Typography.Paragraph>

          <div style={{ minHeight: 50 }}>
            {loading ? (
              <Spin tip="Opening File Manager..." />
            ) : (
              <Button
                type="primary"
                size="large"
                onClick={handleOpenFileBrowser}
                icon={<FolderOutlined />}
              >
                Open File Manager
              </Button>
            )}
          </div>
        </Space>
      </Card>
    </div>
  );
};
