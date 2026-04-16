// UserLayout — the chrome for /jabali-panel/*.
//
// Mirrors AdminLayout's shape but with a user-facing nav and a different
// header colour so there's no visual confusion about which mode you're
// in. The sidebar is a hardcoded array — if an admin item appears here,
// it's a source-code bug, not a runtime misconfiguration.
import { useLogout } from "@refinedev/core";
import {
  HomeOutlined,
  LogoutOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Avatar, Button, Dropdown, Layout, Menu, Typography } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";
import type { MenuProps } from "antd";

import { getIdentity } from "../identity";
import { useEffect, useState } from "react";

const { Header, Sider, Content } = Layout;

const MENU: { key: string; path: string; label: string; icon: JSX.Element }[] = [
  { key: "profile", path: "/jabali-panel/profile", label: "My profile", icon: <UserOutlined /> },
  // future: domains, email, dns, databases, wordpress, ssl, cron, …
];

export function UserLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { mutate: logout } = useLogout();
  const [email, setEmail] = useState<string>("");

  useEffect(() => {
    getIdentity().then((me) => setEmail(me?.email ?? ""));
  }, []);

  const selected = MENU.find((m) =>
    location.pathname.startsWith(m.path),
  )?.key;

  const onMenuClick: MenuProps["onClick"] = ({ key }) => {
    const item = MENU.find((m) => m.key === key);
    if (item) navigate(item.path);
  };

  const userMenu: MenuProps["items"] = [
    {
      key: "logout",
      icon: <LogoutOutlined />,
      label: "Sign out",
      onClick: () => logout(),
    },
  ];

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Sider
        breakpoint="md"
        collapsible
        style={{ background: "#062d4d" }} // slightly brighter blue vs admin's near-black
      >
        <div
          style={{
            color: "white",
            padding: "16px",
            fontSize: 16,
            fontWeight: 600,
            letterSpacing: 0.5,
          }}
        >
          Jabali Panel
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={selected ? [selected] : []}
          onClick={onMenuClick}
          items={MENU.map((m) => ({ key: m.key, icon: m.icon, label: m.label }))}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            background: "#fff",
            padding: "0 24px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            boxShadow: "0 1px 2px rgba(0,0,0,0.04)",
          }}
        >
          <Typography.Text type="secondary">
            <HomeOutlined /> My hosting
          </Typography.Text>
          <Dropdown menu={{ items: userMenu }} placement="bottomRight">
            <Button type="text" icon={<Avatar size="small" icon={<UserOutlined />} />}>
              &nbsp;{email || "…"}
            </Button>
          </Dropdown>
        </Header>
        <Content style={{ background: "#f5f5f5" }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
