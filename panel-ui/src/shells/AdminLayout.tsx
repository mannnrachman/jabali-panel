// AdminLayout.tsx — chrome for the admin shell.
//
// Full-width Header on top (brand + search + user menu), Sider + Content
// below. AntD's stock <Layout> + <Sider> + <Header> + <Content> + <Footer>
// composed directly.
import { useState } from "react";
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
          // AntD's Sider theme="dark" hardcodes the v4 navy #001529 and
          // theme="light" hardcodes #fff — neither matches the algorithm's
          // layout bg. Pin to the active Layout token so the sidebar blends
          // with the content area in both light and dark modes.
          style={{ background: token.colorBgLayout }}
        >
          <Menu
            mode="inline"
            theme={mode}
            selectedKeys={selected ? [selected] : []}
            items={items}
            style={{ border: "none", background: token.colorBgLayout }}
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
