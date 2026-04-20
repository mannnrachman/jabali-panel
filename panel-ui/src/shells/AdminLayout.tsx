// AdminLayout.tsx — chrome for the admin shell.
//
// Uses Refine's <ThemedLayoutV2> directly: the Sider column (with our
// shell-filtered menu) on the left, Header band with search + theme
// toggle + user dropdown to the right of the sider, and the brand
// logo in the Sider's Title slot at the top-left. This is the same
// outer structure as Refine's official templates (e.g. refinefoods)
// — the only overrides are the three slot components.
import { ThemedLayoutV2 } from "@refinedev/antd";
import { Outlet } from "react-router";

import { JabaliFooter } from "../components/JabaliFooter";
import { JabaliHeader } from "../components/JabaliHeader";
import { JabaliTitle } from "../components/JabaliTitle";
import { buildShellSider } from "./shellSider";

const AdminSider = buildShellSider("admin");

// Small wrapper so Refine's TitleProps contract (`{ collapsed }`) maps
// to our JabaliTitle — both admin and user shells render "Jabali" now,
// shell context is already communicated by the URL mount + sidebar.
function AdminTitle({ collapsed }: { collapsed: boolean }) {
  return <JabaliTitle collapsed={collapsed} />;
}

export function AdminLayout() {
  return (
    <ThemedLayoutV2
      Title={AdminTitle}
      Header={JabaliHeader}
      Sider={AdminSider}
      Footer={JabaliFooter}
    >
      <Outlet />
    </ThemedLayoutV2>
  );
}
