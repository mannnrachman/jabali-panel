import { ThemedLayoutV2, ThemedSiderV2, ThemedTitleV2, ThemedHeaderV2 } from "@refinedev/antd";
import { useResource } from "@refinedev/core";
import { Outlet } from "react-router";

export function UserLayout() {
  const { resources } = useResource();

  // Filter resources to only show user items (meta.shell === "user")
  const userResources = (resources || []).filter(
    (r) => (r.meta as Record<string, unknown>)?.shell === "user"
  );

  return (
    <ThemedLayoutV2
      Title={({ collapsed }) => (
        <ThemedTitleV2 collapsed={collapsed} text="Jabali Panel" />
      )}
      Header={ThemedHeaderV2}
      Sider={(props) => (
        <ThemedSiderV2
          {...props}
          render={({ items }) => {
            // Filter sidebar items to only show user resources
            const filteredItems =
              items?.filter((item) => {
                const resource = userResources.find((r) => r.name === item.key);
                return !!resource;
              }) || [];
            return filteredItems;
          }}
        />
      )}
    >
      <Outlet />
    </ThemedLayoutV2>
  );
}
