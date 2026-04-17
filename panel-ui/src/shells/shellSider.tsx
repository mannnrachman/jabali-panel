// shellSider — a ThemedSiderV2 wrapper that filters the menu items by
// resource.meta.shell so the admin shell only lists admin resources and
// vice versa (both shells share a single <Refine> instance).
//
// ThemedSiderV2's `render` prop yields ({ items, logout, collapsed });
// we replace `items` with our own filtered <Menu> while keeping the
// rest of Refine's sider chrome (Title slot, collapse trigger, theme
// colors, paddings, hover transitions).
import { useResource } from "@refinedev/core";
import { ThemedSiderV2 } from "@refinedev/antd";
import { Menu } from "antd";
import type { ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";

interface ResourceMeta {
  label?: string;
  icon?: ReactNode;
  shell?: string;
}

/**
 * Build a <ThemedSiderV2> already bound to the given shell ("admin" |
 * "user"). Pass this as the `Sider` prop of <ThemedLayoutV2>.
 */
export function buildShellSider(shell: "admin" | "user") {
  function ShellSider(
    props: React.ComponentProps<typeof ThemedSiderV2>,
  ) {
    return (
      <ThemedSiderV2
        {...props}
        render={() => <ShellSiderMenu shell={shell} />}
      />
    );
  }
  ShellSider.displayName = `ShellSider(${shell})`;
  return ShellSider;
}

function ShellSiderMenu({ shell }: { shell: "admin" | "user" }) {
  const { resources } = useResource();
  const navigate = useNavigate();
  const location = useLocation();

  const shellResources = (resources || []).filter(
    (r) => ((r.meta as ResourceMeta) ?? {}).shell === shell,
  );

  // Longest-prefix match so nested routes (e.g. /jabali-admin/domains/create)
  // still highlight the parent resource.
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
    <Menu
      mode="inline"
      selectedKeys={selectedKey ? [selectedKey] : []}
      items={items}
      // border: none keeps the sider's inner border from AntD's Menu
      // from doubling up with the sider's own right-edge border.
      style={{ border: "none" }}
    />
  );
}
