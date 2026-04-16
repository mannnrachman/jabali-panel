import { useState } from "react";
import { Button, Popconfirm, message } from "antd";
import { LoginOutlined } from "@ant-design/icons";
import { useInvalidate } from "@refinedev/core";
import { useNavigate } from "react-router";
import { apiClient, setAccessToken } from "../../../apiClient";

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
  const navigate = useNavigate();
  const invalidate = useInvalidate();

  const handleImpersonate = async () => {
    setIsLoading(true);
    try {
      const resp = await apiClient.post<{
        access_token: string;
        expires_at: string;
      }>(`/admin/users/${encodeURIComponent(recordItemId)}/impersonate`);

      setAccessToken(resp.data.access_token);
      message.success(`Impersonating ${userEmail}`);

      // Invalidate the me cache so downstream consumers re-fetch the new identity
      invalidate({
        resource: "me",
        invalidates: ["list"],
      });

      // Navigate to user shell, Authenticated wrapper will re-fetch /me
      navigate("/jabali-panel");
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
      title="Log in as this user?"
      description={`Log in as ${userEmail}? This admin session will be replaced.`}
      onConfirm={handleImpersonate}
      okText="Confirm"
      cancelText="Cancel"
      okButtonProps={{ loading: isLoading }}
    >
      <Button
        size="small"
        icon={<LoginOutlined />}
        onClick={(e) => e.stopPropagation()}
        title="Login as this user"
        style={{ padding: 0 }}
      />
    </Popconfirm>
  );
};
