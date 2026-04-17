// UserLayout.tsx — chrome for the user shell.
//
// Uses Refine's <ThemedLayoutV2> directly with slot overrides — same
// pattern as AdminLayout; only the shell filter + brand text differ.
import { ThemedLayoutV2 } from "@refinedev/antd";
import { Outlet } from "react-router";

import { JabaliHeader } from "../components/JabaliHeader";
import { JabaliTitle } from "../components/JabaliTitle";
import { buildShellSider } from "./shellSider";

const UserSider = buildShellSider("user");

function UserTitle({ collapsed }: { collapsed: boolean }) {
  return <JabaliTitle collapsed={collapsed} text="Jabali Panel" />;
}

export function UserLayout() {
  return (
    <ThemedLayoutV2
      Title={UserTitle}
      Header={JabaliHeader}
      Sider={UserSider}
    >
      <Outlet />
    </ThemedLayoutV2>
  );
}
