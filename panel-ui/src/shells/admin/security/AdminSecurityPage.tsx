// AdminSecurityPage — admin-only Security tab shell (M26 Step 6).
//
// Two sub-tabs, URL-driven via ?tab=crowdsec|ufw. ModSecurity was
// removed (2026-04-26) — CrowdSec AppSec covers the WAF role with no
// duplicate inspection layer. See ADR-0055 (SUPERSEDED).
import { Card, Tabs, Typography } from "antd";
import { LockOutlined } from "@icons";
import type { ReactNode } from "react";
import { useSearchParams } from "react-router";

import crowdsecBrand from "../../../icons/brand/crowdsec.svg";
import { AdminSecurityCrowdsec } from "./AdminSecurityCrowdsec";
import { AdminSecurityUfw } from "./AdminSecurityUfw";

const TAB_KEYS = ["crowdsec", "ufw"] as const;
type TabKey = (typeof TAB_KEYS)[number];

const TAB_LABELS: Record<TabKey, string> = {
  crowdsec: "CrowdSec",
  ufw: "Firewall (UFW)",
};

// CrowdSec uses the upstream brand mark (homarr-labs/dashboard-icons,
// MIT). Rendered as an <img> at 1em so it lines up with the AntD
// label baseline like the lucide shims do; keeping the original
// brand colors instead of forcing currentColor.
const CrowdsecBrandIcon = () => (
  <img
    src={crowdsecBrand}
    alt=""
    style={{ width: "1em", height: "1em", verticalAlign: "-0.125em" }}
  />
);

const TAB_ICONS: Record<TabKey, ReactNode> = {
  crowdsec: <CrowdsecBrandIcon />,
  ufw: <LockOutlined />,
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
            icon: TAB_ICONS[k],
            label: TAB_LABELS[k],
            children: activeTab === k ? renderTab() : null,
          }))}
        />
      </Card>
    </div>
  );
};
