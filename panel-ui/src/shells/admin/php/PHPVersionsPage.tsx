import { useState } from "react";
import { Card, Typography } from "antd";
import { CodeOutlined } from "@icons";
import { VersionsTab } from "./VersionsTab";
import { PHPExtensionsTab } from "./PHPExtensionsTab";

type TabKey = "versions" | "extensions";

export const PHPVersionsPage = () => {
  const [active, setActive] = useState<TabKey>("versions");

  return (
    <div >
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        <CodeOutlined /> PHP Manager
      </Typography.Title>

      {/* Card.tabList pins the tab strip to the top-left of the card body —
          mirrors the Server Settings page for consistency across admin pages. */}
      <Card
        tabList={[
          { key: "versions", tab: "PHP Versions" },
          { key: "extensions", tab: "PHP Extensions" },
        ]}
        activeTabKey={active}
        onTabChange={(k) => setActive(k as TabKey)}
      >
        {active === "versions" ? <VersionsTab /> : <PHPExtensionsTab />}
      </Card>
    </div>
  );
};
