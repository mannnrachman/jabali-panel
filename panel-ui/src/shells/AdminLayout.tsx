// AdminLayout — the chrome for /jabali-admin/*.
//
// Everything here is explicit — the sidebar is a hardcoded menu, not a
// derived-from-resources list. That's deliberate: it's impossible for a
// user-panel item to appear in an admin sidebar by accident, and the
// file itself is the single source of truth for "what do admins see".
//
// UserLayout mirrors this shape; keeping them separate (even with some
// duplication) beats a single generic layout with a "which mode are we
// in?" boolean branching inside.
import { useLogout } from "@refinedev/core";
import {
  AppstoreOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  GlobalOutlined,
  LogoutOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Avatar, Button, Dropdown, Layout, Menu, Typography } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";
import type { MenuProps } from "antd";

import { getIdentity } from "../identity";
import { useEffect, useState } from "react";

const { Header, Sider, Content } = Layout;

// Admin nav. Keep this array the single source of truth for what shows in
// the admin sidebar — adding a new admin page = add an item here.
const MENU: { key: string; path: string; label: string; icon: JSX.Element }[] = [
  { key: "dashboard", path: "/jabali-admin/dashboard", label: "Dashboard", icon: <DashboardOutlined /> },
  { key: "users", path: "/jabali-admin/users", label: "Users", icon: <TeamOutlined /> },
  { key: "packages", path: "/jabali-admin/packages", label: "Packages", icon: <AppstoreOutlined /> },
  { key: "domains", path: "/jabali-admin/domains", label: "Domains", icon: <GlobalOutlined /> },
  { key: "settings", path: "/jabali-admin/settings", label: "Settings", icon: <CloudServerOutlined /> },
];

export function AdminLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { mutate: logout } = useLogout();
  const [email, setEmail] = useState<string>("");

  useEffect(() => {
    getIdentity().then((me) => setEmail(me?.email ?? ""));
  }, []);

  // Pick the selected menu key based on the first path segment after
  // /jabali-admin/. /jabali-admin/users, /jabali-admin/users/create, etc.
  // all highlight "users".
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
        style={{ background: "#001529" }}
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
          Jabali Admin
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
            <SettingOutlined /> Administration
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
