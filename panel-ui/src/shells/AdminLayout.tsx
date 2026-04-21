// AdminLayout.tsx — chrome for the admin shell.
//
// Full-width Header on top (brand + search + user menu), Sider + Content
// below. AntD's stock <Layout> + <Sider> + <Header> + <Content> + <Footer>
// composed directly.
import { useState } from "react";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import { Layout, Menu, theme } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { adminNav, selectedNavKey } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function AdminLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();
  const { token } = theme.useToken();

  const items = adminNav.map((n) => ({
    key: n.key,
    icon: n.icon,
    label: n.label,
    onClick: () => navigate(n.path),
  }));

  const selected = selectedNavKey(adminNav, location.pathname);

  // Light mode: explicit Tailwind gray-50 / gray-100 per operator request
  // so the sidebar sits a shade paler than the main card surface and the
  // active menu row reads slightly darker than the sidebar body. Dark
  // mode keeps the layout-bg token (it already pairs well with the
  // algorithm-derived itemSelectedBg).
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
          // Replace AntD's built-in trigger bar (which paints a navy or
          // white strip regardless of algorithm) with a bare chevron
          // icon — triggerBg: transparent alone isn't enough once a
          // border-top rule sneaks in.
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
          style={{ background: siderBg, paddingTop: 16 }}
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
