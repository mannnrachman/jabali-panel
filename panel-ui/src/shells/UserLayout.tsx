// UserLayout — the chrome for /jabali-panel/*.
//
// Mirrors AdminLayout's shape but with a user-facing nav and a different
// header colour so there's no visual confusion about which mode you're
// in. The sidebar is a hardcoded array — if an admin item appears here,
// it's a source-code bug, not a runtime misconfiguration.
import { useLogout } from "@refinedev/core";
import {
  GlobalOutlined,
  HomeOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Avatar, Button, Dropdown, Layout, Menu, Typography } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";
import type { MenuProps } from "antd";

import { getIdentity } from "../identity";
import { useEffect, useState } from "react";
import { ThemeToggle } from "../components/ThemeToggle";
import { useThemeMode } from "../theme/ThemeModeContext";
import { useShellTokens } from "../muiTheme";

const { Header, Sider, Content } = Layout;

const MENU: { key: string; path: string; label: string; icon: JSX.Element }[] = [
  { key: "profile", path: "/jabali-panel/profile", label: "My profile", icon: <UserOutlined /> },
  { key: "domains", path: "/jabali-panel/domains", label: "Domains", icon: <GlobalOutlined /> },
  { key: "dns", path: "/jabali-panel/dns", label: "DNS", icon: <GlobalOutlined /> },
  { key: "ssl", path: "/jabali-panel/ssl", label: "SSL", icon: <SafetyCertificateOutlined /> },
  // future: email, databases, wordpress, cron, …
];

export function UserLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { mutate: logout } = useLogout();
  const { mode } = useThemeMode();
  const tokens = useShellTokens(mode);
  // User shell keeps a brighter blue sider in both modes so the visual
  // distinction vs AdminLayout survives the theme switch.
  const userSiderBg = mode === "dark" ? "#0e2238" : "#062d4d";
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
        style={{ background: userSiderBg }} // slightly brighter blue vs admin's near-black
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
            background: tokens.headerBg,
            padding: "0 24px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            borderBottom: tokens.headerBorder,
          }}
        >
          <Typography.Text type="secondary">
            <HomeOutlined /> My hosting
          </Typography.Text>
          <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
            <ThemeToggle />
            <Dropdown menu={{ items: userMenu }} placement="bottomRight">
              <Button type="text" icon={<Avatar size="small" icon={<UserOutlined />} />}>
                &nbsp;{email || "…"}
              </Button>
            </Dropdown>
          </div>
        </Header>
        <Content style={{ background: tokens.contentBg }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
