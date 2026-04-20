// AdminLayout.tsx — chrome for the admin shell.
//
// Plain AntD <Layout> + <Sider> + <Header> + <Content> + <Footer>
// composed directly. Refine's <ThemedLayoutV2> is gone; the visual
// arrangement (brand at top-left, menu below, header band with
// search + theme toggle + avatar, content + footer) is the same.
import { useState } from "react";
import { Layout, Menu } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { JabaliTitle } from "../components/JabaliTitle";
import { adminNav, selectedNavKey } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function AdminLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();

  const items = adminNav.map((n) => ({
    key: n.key,
    icon: n.icon,
    label: n.label,
    onClick: () => navigate(n.path),
  }));

  const selected = selectedNavKey(adminNav, location.pathname);

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
