// AdminLayout.tsx — chrome for the admin shell.
//
// Layout: full-width header across the top, sider + content below.
// ThemedLayoutV2 was painting the header beside the sider and forcing a
// "Refine Project" title box in the top-left — both fixed here by
// using AntD's plain Layout primitives and driving our own nav items
// from the shell-scoped resource list.
//
// Styling: sider + menu colors match ThemedLayoutV2 by using AntD tokens
// so both light and dark themes look consistent with Refine's default.
import { useResource } from "@refinedev/core";
import { Layout, Menu, theme } from "antd";
import type { ReactNode } from "react";
import { useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router";

import { JabaliHeader } from "../components/JabaliHeader";

const { Sider, Content } = Layout;

interface ResourceMeta {
  label?: string;
  icon?: ReactNode;
  shell?: string;
}

export function AdminLayout() {
  const { resources } = useResource();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const { token } = theme.useToken();

  // Filter resources to this shell; meta.shell is the only thing
  // distinguishing admin nav from user nav since both shells share
  // one <Refine> instance.
  const shellResources = (resources || []).filter(
    (r) => ((r.meta as ResourceMeta) ?? {}).shell === "admin",
  );

  // Longest-prefix match so nested routes
  // (e.g. /jabali-admin/domains/create) highlight "domains".
  const selectedKey =
    [...shellResources]
      .sort((a, b) => (b.list as string).length - (a.list as string).length)
      .find((r) => location.pathname.startsWith(r.list as string))?.name ?? "";

  const items = shellResources.map((r) => {
    const meta = (r.meta as ResourceMeta) ?? {};
    return {
      key: r.name,
      icon: meta.icon,
      label: meta.label ?? r.name,
      onClick: () => {
        if (typeof r.list === "string") navigate(r.list);
      },
    };
  });

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <JabaliHeader brand="Jabali Admin" />
      <Layout>
        <Sider
          width={220}
          breakpoint="md"
          collapsible
          collapsed={collapsed}
          onCollapse={setCollapsed}
          style={{
            backgroundColor: token.colorBgContainer,
            borderRight: `1px solid ${token.colorBgElevated}`,
          }}
        >
          <Menu
            mode="inline"
            selectedKeys={selectedKey ? [selectedKey] : []}
            items={items}
            style={{
              paddingTop: "8px",
              border: "none",
              backgroundColor: token.colorBgContainer,
            }}
          />
        </Sider>
        <Content>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
