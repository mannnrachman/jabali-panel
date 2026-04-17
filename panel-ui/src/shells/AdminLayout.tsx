import { ThemedLayoutV2, ThemedSiderV2, ThemedTitleV2, ThemedHeaderV2 } from "@refinedev/antd";
import { useResource } from "@refinedev/core";
import { Outlet } from "react-router";

export function AdminLayout() {
  const { resources } = useResource();

  // Filter resources to only show admin items (meta.shell === "admin")
  const adminResources = (resources || []).filter(
    (r) => (r.meta as Record<string, unknown>)?.shell === "admin"
  );

  return (
    <ThemedLayoutV2
      Title={({ collapsed }) => (
        <ThemedTitleV2 collapsed={collapsed} text="Jabali Admin" />
      )}
      Header={ThemedHeaderV2}
      Sider={(props) => (
        <ThemedSiderV2
          {...props}
          render={({ items }) => {
            // Map Refine resources to sidebar items compatible with Refine's default rendering
            const filterediItems =
              items?.filter((item) => {
                const resource = adminResources.find((r) => r.name === item.key);
                return !!resource;
              }) || [];
            return filterediItems;
          }}
        />
      )}
    >
      <Outlet />
    </ThemedLayoutV2>
  );
}
