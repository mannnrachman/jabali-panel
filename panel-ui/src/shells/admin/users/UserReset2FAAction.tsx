// UserReset2FAAction — admin button to strip TOTP + recovery codes from
// a user's Kratos identity. Used when a user has lost their authenticator
// AND burned through their recovery codes. The user keeps their password;
// on next login they're at aal1 and can re-enrol from /profile.
import { useState } from "react";
import { Button, Modal, message } from "antd";
import { SafetyOutlined } from "@icons";

import { apiClient } from "../../../apiClient";

interface UserReset2FAActionProps {
  userId: string;
  userEmail: string;
}

export const UserReset2FAAction = ({
  userId,
  userEmail,
}: UserReset2FAActionProps) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  const handleReset = async () => {
    setIsLoading(true);
    try {
      await apiClient.post(`/admin/users/${encodeURIComponent(userId)}/2fa/reset`);
      message.success(`Two-factor authentication reset for "${userEmail}"`);
      setIsModalOpen(false);
    } catch (err: unknown) {
      const errMsg =
        err instanceof Error ? err.message : "Failed to reset two-factor authentication";
      message.error(errMsg);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <>
      <Button
        variant="filled"
        color="primary"
        icon={<SafetyOutlined />}
        onClick={() => setIsModalOpen(true)}
      >
        Reset 2FA
      </Button>
      <Modal
        title="Reset two-factor authentication?"
        open={isModalOpen}
        onCancel={() => setIsModalOpen(false)}
        onOk={handleReset}
        confirmLoading={isLoading}
        okText="Reset"
        okButtonProps={{ danger: true }}
      >
        <p>
          Removes the TOTP authenticator and recovery codes from{" "}
          <strong>{userEmail}</strong>. The user keeps their password and can
          re-enrol from their profile page after their next sign-in.
        </p>
        <p>Use only when the user has confirmed they cannot recover access.</p>
      </Modal>
    </>
  );
};
