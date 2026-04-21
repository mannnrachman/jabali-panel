// UserLayout.tsx — chrome for the user shell.
//
// Same composition as AdminLayout (see that file for the "why"), but
// driven by `userNav` so an admin-only entry can never leak into the
// sidebar here.
import { useState } from "react";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import { Layout, Menu, theme } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { selectedNavKey, userNav } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function UserLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();
  const { token } = theme.useToken();

  const items = userNav.map((n) => ({
    key: n.key,
    icon: n.icon,
    label: n.label,
    onClick: () => navigate(n.path),
  }));

  const selected = selectedNavKey(userNav, location.pathname);

  const siderBg = mode === "dark" ? token.colorBgLayout : "#f9fafb";

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <JabaliHeader />
      <Layout>
        <Sider
          theme={mode}
          width={256}
          breakpoint="lg"
          collapsedWidth="64"
          collapsible
          collapsed={collapsed}
          onCollapse={setCollapsed}
          trigger={
            <span
              style={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                color: token.colorTextSecondary,
                background: "transparent",
              }}
            >
              {collapsed ? <RightOutlined /> : <LeftOutlined />}
            </span>
          }
          style={{ background: siderBg, paddingTop: 16, paddingInline: 8 }}
        >
          <Menu
            mode="inline"
            theme={mode}
            selectedKeys={selected ? [selected] : []}
            items={items}
            style={{ border: "none", background: siderBg }}
          />
        </Sider>
        <Layout>
          <Content style={{ padding: 24 }}>
            <Outlet />
          </Content>
          <JabaliFooter />
        </Layout>
      </Layout>
    </Layout>
  );
}
