// AdminLayout.tsx — chrome for the admin shell.
//
// e9fc63c replaced the hand-rolled sider with ThemedSiderV2 + a filter
// on items[].key, but in Refine v4 the rendered item's key isn't the
// bare resource name, so the filter dropped every item and sidebars
// came up empty. Rather than re-derive the key mapping, we build the
// <Menu> ourselves from the shell-filtered resources and hand it to
// ThemedSiderV2's render prop — keeps the sider chrome (collapse,
// theme integration) while giving us exact control over what shows.
import { ThemedLayoutV2, ThemedSiderV2 } from "@refinedev/antd";
import { JabaliHeader } from "../components/JabaliHeader";
import { JabaliTitle } from "../components/JabaliTitle";
import { useResource } from "@refinedev/core";
import { Menu } from "antd";
import type { ReactNode } from "react";
import { Outlet, useLocation, useNavigate } from "react-router";

interface ResourceMeta {
  label?: string;
  icon?: ReactNode;
  shell?: string;
}

export function AdminLayout() {
  const { resources } = useResource();
  const navigate = useNavigate();
  const location = useLocation();

  // The meta.shell discriminator is the only thing distinguishing admin
  // nav from user nav since both shells share one <Refine> instance.
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
    <ThemedLayoutV2
      Title={({ collapsed }) => (
        <JabaliTitle collapsed={collapsed} text="Jabali Admin" />
      )}
      Header={JabaliHeader}
      Sider={(siderProps) => (
        <ThemedSiderV2
          {...siderProps}
          render={() => (
            <Menu
              theme="dark"
              mode="inline"
              selectedKeys={selectedKey ? [selectedKey] : []}
              items={items}
              style={{ background: "transparent" }}
            />
          )}
        />
      )}
    >
      <Outlet />
    </ThemedLayoutV2>
  );
}
