// AdminSecurityPage — admin-only Security tab shell (M26 Step 6).
//
// Three sub-tabs, URL-driven via ?tab=crowdsec|modsec|ufw. Each tab's
// content lives in a sibling component; this file is just the
// chrome.
import { Tabs, Typography } from "antd";
import { useSearchParams } from "react-router";

import { AdminSecurityCrowdsec } from "./AdminSecurityCrowdsec";
import { AdminSecurityModsec } from "./AdminSecurityModsec";
import { AdminSecurityUfw } from "./AdminSecurityUfw";

const TAB_KEYS = ["crowdsec", "modsec", "ufw"] as const;
type TabKey = (typeof TAB_KEYS)[number];

const isTabKey = (s: string | null): s is TabKey =>
  s !== null && (TAB_KEYS as readonly string[]).includes(s);

export const AdminSecurityPage = () => {
  const [params, setParams] = useSearchParams();
  const activeTab: TabKey = isTabKey(params.get("tab")) ? (params.get("tab") as TabKey) : "crowdsec";

  const onChange = (key: string) => {
    if (isTabKey(key)) {
      setParams((prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", key);
        return next;
      });
    }
  };

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Security
      </Typography.Title>
      <Tabs
        activeKey={activeTab}
        onChange={onChange}
        items={[
          { key: "crowdsec", label: "CrowdSec", children: <AdminSecurityCrowdsec /> },
          { key: "modsec", label: "ModSecurity", children: <AdminSecurityModsec /> },
          { key: "ufw", label: "Firewall (UFW)", children: <AdminSecurityUfw /> },
        ]}
      />
    </div>
  );
};
