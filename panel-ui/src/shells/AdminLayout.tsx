// AdminLayout.tsx — chrome for the admin shell.
//
// Full-width Header on top (brand + search + user menu), then either
// a persistent <Sider> (≥lg / 992px) or an off-canvas <Drawer> (<lg)
// that the header's hamburger button opens. See ADR-0046.
import { useEffect, useState } from "react";
import { LeftOutlined, RightOutlined } from "@icons";
import { Drawer, Grid, Layout, Menu, theme } from "antd";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { adminNav, selectedNavKey } from "../nav";
import { useThemeMode } from "../theme/ThemeModeContext";

const { Sider, Content } = Layout;

export function AdminLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const { mode } = useThemeMode();
  const { token } = theme.useToken();
  const screens = Grid.useBreakpoint();
  // lg is explicitly nullable (false | undefined) — fall back to desktop
  // layout on first render before breakpoints resolve to avoid a drawer
  // flash for users who actually sit on lg+.
  const isDesktop = screens.lg !== false;

  const selected = selectedNavKey(adminNav, location.pathname);

  // Light mode: explicit Tailwind gray-50 / gray-100 per operator request
  // so the sidebar sits a shade paler than the main card surface and the
  // active menu row reads slightly darker than the sidebar body. Dark
  // mode keeps the layout-bg token (it already pairs well with the
  // algorithm-derived itemSelectedBg).
  const siderBg = mode === "dark" ? token.colorBgLayout : "#f9fafb";

  // Single source of truth for the menu items — used by both <Sider>
  // and <Drawer> so the two shell variants stay in lock-step.
  const menu = (
    <Menu
      mode="inline"
      theme={mode}
      selectedKeys={selected ? [selected] : []}
      style={{ border: "none", background: siderBg }}
      items={adminNav.map((n) => ({
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

  // Close the drawer on every route change — covers not just menu
  // clicks but also back-button / programmatic navigation.
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
                  background: token.colorFillSecondary,
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
          <Content
            style={{
              // Extra top gap so the page heading breathes away from
              // the header's bottom border. Horizontal + bottom stay
              // at the baseline gutter.
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
