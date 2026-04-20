// UserLayout.tsx — chrome for the user shell.
//
// Same composition as AdminLayout (see that file for the "why"), but
// driven by `userNav` so an admin-only entry can never leak into the
// sidebar here.
import { useState } from "react";
import { Layout, Menu } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { JabaliTitle } from "../components/JabaliTitle";
import { selectedNavKey, userNav } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function UserLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();

  const items = userNav.map((n) => ({
    key: n.key,
    icon: n.icon,
    label: n.label,
    onClick: () => navigate(n.path),
  }));

  const selected = selectedNavKey(userNav, location.pathname);

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Sider
        theme={mode}
        width={256}
        breakpoint="lg"
        collapsedWidth="64"
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
      >
        <div style={{ padding: "16px 12px" }}>
          <JabaliTitle collapsed={collapsed} />
        </div>
        <Menu
          mode="inline"
          theme={mode}
          selectedKeys={selected ? [selected] : []}
          items={items}
          style={{ border: "none" }}
        />
      </Sider>
      <Layout>
        <JabaliHeader />
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
        <JabaliFooter />
      </Layout>
    </Layout>
  );
}
