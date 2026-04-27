// UserLayout.tsx — chrome for the user shell.
//
// Same composition as AdminLayout (see that file for the "why"), but
// driven by `userNav` so an admin-only entry can never leak into the
// sidebar here.
import { useEffect, useState } from "react";
import { LeftOutlined, RightOutlined } from "@icons";
import { ConfigProvider, Drawer, Grid, Layout, Menu, theme } from "antd";
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

  // User panel takes the AntD-default blue accent on the selected menu
  // row; admin keeps red (set globally in muiTheme.ts). The nested
  // ConfigProvider overlays the Menu tokens for this shell only —
  // header, footer, tabs, and buttons still read the red accent from
  // the top-level provider because they inherit outside this wrap.
  const menu = (
    <ConfigProvider
      theme={{
        components: {
          Menu:
            mode === "dark"
              ? {
                  darkItemSelectedBg: "#1f1f1f",
                  darkItemSelectedColor: "#4096ff",
                  darkItemHoverBg: "#1f1f1f",
                  darkItemHoverColor: "rgba(255, 255, 255, 0.85)",
                }
              : {
                  itemSelectedBg: "#f3f4f6",
                  itemSelectedColor: "#1677ff",
                  itemHoverBg: "#f3f4f6",
                  itemHoverColor: "rgba(0, 0, 0, 0.88)",
                },
        },
      }}
    >
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
    </ConfigProvider>
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
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  width: "100%",
                  height: "100%",
                  color: token.colorTextSecondary,
                  background: siderBg,
                  borderTop: `1px solid ${token.colorBorderSecondary}`,
                }}
              >
                {collapsed ? <RightOutlined /> : <LeftOutlined />}
              </div>
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
          <Content
            style={{
              padding: screens.md ? "32px 24px 24px" : "20px 12px 12px",
            }}
          >
            <Outlet />
          </Content>
          <JabaliFooter />
        </Layout>
      </Layout>
    </Layout>
  );
}
