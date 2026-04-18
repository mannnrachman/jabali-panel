import { useEffect, useState } from "react";
import { Alert, Button, Spin } from "antd";
import { FolderOutlined } from "@ant-design/icons";
import { ssoFileBrowser } from "../../../apiClient";

// Inline file-manager page: fetch the one-shot SSO URL on mount and
// embed it in an iframe that fills the shell's content area. The
// SSO token is single-use so we refetch whenever the user lands here.
export const UserFilesLauncher = () => {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setError(null);
    setLoading(true);
    try {
      const resp = await ssoFileBrowser();
      setUrl(resp.redirect_url);
    } catch (err) {
      setUrl(null);
      setError(err instanceof Error ? err.message : "Could not open File Manager");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  if (loading) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "calc(100vh - 120px)",
        }}
      >
        <Spin tip="Opening File Manager..." size="large" />
      </div>
    );
  }

  if (error || !url) {
    return (
      <div style={{ padding: 24, maxWidth: 600, margin: "40px auto" }}>
        <Alert
          type="error"
          showIcon
          message="Could not open File Manager"
          description={error ?? "Unexpected error"}
          action={
            <Button size="small" onClick={load} icon={<FolderOutlined />}>
              Retry
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <iframe
      src={url}
      title="File Manager"
      style={{
        width: "100%",
        height: "calc(100vh - 80px)",
        border: "none",
        display: "block",
      }}
    />
  );
};
