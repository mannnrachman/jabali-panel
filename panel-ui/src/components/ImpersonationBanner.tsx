import { useEffect, useState } from "react";
import { Alert, Button, Space } from "antd";
import { LogoutOutlined } from "@ant-design/icons";
import { useLogout } from "@refinedev/core";
import { getIdentity } from "../identity";

/**
 * Displays a red banner when the current session is impersonating another user.
 * Allows a one-click exit (logout) to return to the login screen.
 * Returns null when not impersonating.
 */
export function ImpersonationBanner() {
  const [email, setEmail] = useState<string | null>(null);
  const { mutate: logout } = useLogout();

  useEffect(() => {
    getIdentity().then((me) => {
      if (me?.impersonatedBy) {
        setEmail(me.email);
      }
    });
  }, []);

  if (!email) {
    return null;
  }

  return (
    <Alert
      type="error"
      banner
      message={
        <Space>
          <span>You are impersonating {email}</span>
          <Button
            type="text"
            danger
            size="small"
            icon={<LogoutOutlined />}
            onClick={() => logout()}
          >
            Exit
          </Button>
        </Space>
      }
      style={{ marginBottom: 0 }}
    />
  );
}
