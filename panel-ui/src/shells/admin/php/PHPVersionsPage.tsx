import { useState } from "react";
import { Card, Typography } from "antd";
import { VersionsTab } from "./VersionsTab";
import { PHPExtensionsTab } from "./PHPExtensionsTab";

type TabKey = "versions" | "extensions";

export const PHPVersionsPage = () => {
  const [active, setActive] = useState<TabKey>("versions");

  return (
    <div >
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        PHP Manager
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Install PHP versions and manage extensions per version.
      </Typography.Paragraph>

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
