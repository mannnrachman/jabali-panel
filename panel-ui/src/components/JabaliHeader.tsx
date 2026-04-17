// JabaliHeader — replacement for @refinedev/antd's ThemedHeaderV2.
//
// The built-in header doesn't expose a slot for extra actions, so we
// render our own with: theme toggle, email-as-dropdown, logout. Styling
// tracks AntD's Header tokens via useToken so the colour scheme stays
// consistent with whichever theme mode is active.
import { useEffect, useState } from "react";
import { LogoutOutlined, UserOutlined } from "@ant-design/icons";
import { Avatar, Button, Dropdown, Layout, theme } from "antd";
import type { MenuProps } from "antd";
import { useLogout } from "@refinedev/core";

import { getIdentity } from "../identity";
import { ThemeToggle } from "./ThemeToggle";

const { Header } = Layout;

export function JabaliHeader() {
  const { mutate: logout } = useLogout();
  const { token } = theme.useToken();
  const [email, setEmail] = useState<string>("");

  useEffect(() => {
    getIdentity().then((me) => setEmail(me?.email ?? ""));
  }, []);

  const userMenu: MenuProps["items"] = [
    {
      key: "logout",
      icon: <LogoutOutlined />,
      label: "Sign out",
      onClick: () => logout(),
    },
  ];

  return (
    <Header
      style={{
        background: token.colorBgContainer,
        padding: "0 24px",
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-end",
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
        // Refine's ThemedLayoutV2 uses position: sticky by default; match
        // that so the header stays pinned when the content scrolls.
        position: "sticky",
        top: 0,
        zIndex: 1,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
        <ThemeToggle />
        <Dropdown menu={{ items: userMenu }} placement="bottomRight">
          <Button type="text" icon={<Avatar size="small" icon={<UserOutlined />} />}>
            &nbsp;{email || "…"}
          </Button>
        </Dropdown>
      </div>
    </Header>
  );
}
