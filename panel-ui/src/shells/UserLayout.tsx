// UserLayout.tsx — chrome for the user shell.
//
// Same composition as AdminLayout (see that file for the "why"), but
// driven by `userNav` so an admin-only entry can never leak into the
// sidebar here.
import { useEffect, useState } from "react";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import { Drawer, Grid, Layout, Menu, theme } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { selectedNavKey, userNav } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function UserLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg !== false;

  const selected = selectedNavKey(userNav, location.pathname);

  const siderBg = mode === "dark" ? token.colorBgLayout : "#f9fafb";

  const menu = (
    <Menu
      mode="inline"
      theme={mode}
      selectedKeys={selected ? [selected] : []}
      style={{ border: "none", background: siderBg }}
      items={userNav.map((n) => ({
        key: n.key,
        icon: n.icon,
        label: n.label,
        onClick: () => {
          navigate(n.path);
          setDrawerOpen(false);
        },
      }))}
    />
  );

  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <JabaliHeader
        showMenuButton={!isDesktop}
        onMenuClick={() => setDrawerOpen(true)}
      />
      <Layout>
        {isDesktop ? (
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
            {menu}
          </Sider>
        ) : (
          <Drawer
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            placement="left"
            width={256}
            closable={false}
            styles={{ body: { padding: 8, background: siderBg } }}
          >
            {menu}
          </Drawer>
        )}
        <Layout>
          <Content style={{ padding: screens.md ? 24 : 12 }}>
            <Outlet />
          </Content>
          <JabaliFooter />
        </Layout>
      </Layout>
    </Layout>
  );
}
