import { useState } from "react";
import { Button, Popconfirm, message } from "antd";
import { LoginOutlined } from "@ant-design/icons";
import { apiClient } from "../../../apiClient";

interface UserImpersonateActionProps {
  recordItemId: string;
  userEmail: string;
  isAdmin: boolean;
}

export const UserImpersonateAction = ({
  recordItemId,
  userEmail,
  isAdmin,
}: UserImpersonateActionProps) => {
  const [isLoading, setIsLoading] = useState(false);

  const handleImpersonate = async () => {
    setIsLoading(true);
    try {
      const resp = await apiClient.post<{
        login_url: string;
      }>(`/admin/users/${encodeURIComponent(recordItemId)}/impersonate`);

      message.success(`Opening login link for ${userEmail}`);

      // Open the login URL in a new tab (one-shot session)
      window.open(resp.data.login_url, "_blank");
    } catch (err: unknown) {
      const errMsg =
        err instanceof Error ? err.message : "Failed to impersonate user";
      message.error(errMsg);
    } finally {
      setIsLoading(false);
    }
  };

  // Hide button for admins (can't impersonate another admin)
  if (isAdmin) {
    return null;
  }

  return (
    <Popconfirm
      title="Open login link for this user?"
      description={`Open a login link for ${userEmail}? A new tab will open with a temporary login session.`}
      onConfirm={handleImpersonate}
      okText="Open in New Tab"
      cancelText="Cancel"
      okButtonProps={{ loading: isLoading }}
    >
      <Button
        type="text"
        size="small"
        icon={<LoginOutlined />}
        onClick={(e) => e.stopPropagation()}
        title="Login as this user"
        style={{ padding: 0 }}
      />
    </Popconfirm>
  );
};
