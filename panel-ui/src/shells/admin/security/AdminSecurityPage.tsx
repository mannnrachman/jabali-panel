// AdminSecurityPage — admin-only Security page (M26 Step 6).
//
// Two top tabs (CrowdSec, Firewall) on the Card.tabList pattern that
// Mail and Notifications use, so the chrome matches the rest of the
// panel. Sub-tabs and section cards live inside each tab's component
// — those keep the existing Tabs / Card size="small" structure since
// they're content groupings, not page chrome. ModSecurity was removed
// (2026-04-26): CrowdSec AppSec covers the WAF role with no duplicate
// inspection layer (ADR-0055 SUPERSEDED).
import { Card, Space, Typography } from "antd";
import { BugOutlined, LockOutlined, SafetyOutlined, ApiOutlined, ShieldCheckOutlined, SearchOutlined } from "@icons";
import type { ReactNode } from "react";
import { useSearchParams } from "react-router";

import crowdsecBrand from "../../../icons/brand/crowdsec.svg";

import { AdminSecurityAide } from "./AdminSecurityAide";
import { AdminSecurityAppArmor } from "./AdminSecurityAppArmor";
import { AdminSecuritySnuffleupagus } from "./AdminSecuritySnuffleupagus";
import { AdminSecurityCrowdsec } from "./AdminSecurityCrowdsec";
import { AdminSecurityEgress } from "./AdminSecurityEgress";
import { AdminSecurityMalware } from "./AdminSecurityMalware";
import { AdminSecurityUfw } from "./AdminSecurityUfw";

const TAB_KEYS = ["crowdsec", "malware", "snuffleupagus", "ufw", "egress", "apparmor", "aide"] as const;
type TabKey = (typeof TAB_KEYS)[number];
const DEFAULT_TAB: TabKey = "crowdsec";

const TAB_LABELS: Record<TabKey, string> = {
  crowdsec: "CrowdSec",
  malware: "Malware",
  ufw: "Firewall (UFW)",
  egress: "Egress (per-user)",
  snuffleupagus: "Snuffleupagus",
  apparmor: "AppArmor",
  aide: "AIDE",
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
  malware: <BugOutlined />,
  ufw: <LockOutlined />,
  egress: <ApiOutlined />,
  snuffleupagus: <ShieldCheckOutlined />,
  apparmor: <ShieldCheckOutlined />,
  aide: <SearchOutlined />,
};

const isTabKey = (s: string | null): s is TabKey =>
  s !== null && (TAB_KEYS as readonly string[]).includes(s);

export const AdminSecurityPage = () => {
  const [params, setParams] = useSearchParams();
  const activeKey: TabKey = isTabKey(params.get("tab"))
    ? (params.get("tab") as TabKey)
    : DEFAULT_TAB;

  const onChange = (key: string) => {
    if (!isTabKey(key)) return;
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("tab", key);
      // Drop the sub-tab key when the top tab changes — sub state is
      // owned per-component and a stale ?sub= would point at a tab
      // that doesn't exist in the new component.
      next.delete("sub");
      return next;
    });
  };

  const renderTab = () => {
    switch (activeKey) {
      case "crowdsec":
        return <AdminSecurityCrowdsec />;
      case "malware":
        return <AdminSecurityMalware />;
      case "ufw":
        return <AdminSecurityUfw />;
      case "egress":
        return <AdminSecurityEgress />;
      case "snuffleupagus":
        return <AdminSecuritySnuffleupagus />;
      case "apparmor":
        return <AdminSecurityAppArmor />;
      case "aide":
        return <AdminSecurityAide />;
    }
  };

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        <SafetyOutlined /> Security
      </Typography.Title>

      <Card
        tabList={TAB_KEYS.map((k) => ({
          key: k,
          tab: (
            <Space size={6}>
              {TAB_ICONS[k]}
              {TAB_LABELS[k]}
            </Space>
          ),
        }))}
        activeTabKey={activeKey}
        onTabChange={onChange}
      >
        {renderTab()}
      </Card>
    </div>
  );
};
