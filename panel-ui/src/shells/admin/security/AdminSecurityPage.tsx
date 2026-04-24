// AdminSecurityPage — admin-only Security tab shell (M26 Step 6).
//
// Three sub-tabs, URL-driven via ?tab=crowdsec|modsec|ufw. Uses the
// Card.tabList pattern (same as MailTabsPage / NotificationsTabsPage)
// so the tab strip is attached to a card body rather than floating
// above the page.
import { Card, Tabs, Typography } from "antd";
import { useSearchParams } from "react-router";

import { AdminSecurityCrowdsec } from "./AdminSecurityCrowdsec";
import { AdminSecurityModsec } from "./AdminSecurityModsec";
import { AdminSecurityUfw } from "./AdminSecurityUfw";

const TAB_KEYS = ["crowdsec", "modsec", "ufw"] as const;
type TabKey = (typeof TAB_KEYS)[number];

const TAB_LABELS: Record<TabKey, string> = {
  crowdsec: "CrowdSec",
  modsec: "ModSecurity",
  ufw: "Firewall (UFW)",
};

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

  const renderTab = () => {
    switch (activeTab) {
      case "crowdsec":
        return <AdminSecurityCrowdsec />;
      case "modsec":
        return <AdminSecurityModsec />;
      case "ufw":
        return <AdminSecurityUfw />;
    }
  };

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        Security
      </Typography.Title>
      <Card styles={{ body: { padding: 16 } }}>
        <Tabs
          activeKey={activeTab}
          onChange={onChange}
          style={{ marginTop: -8 }}
          items={TAB_KEYS.map((k) => ({
            key: k,
            label: TAB_LABELS[k],
            children: activeTab === k ? renderTab() : null,
          }))}
        />
      </Card>
    </div>
  );
};
