import { useState } from "react";
import { Card, Typography } from "antd";
import {
  ApiOutlined,
  CodeOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { VersionsTab } from "./VersionsTab";
import { PHPExtensionsTab } from "./PHPExtensionsTab";

type TabKey = "versions" | "extensions";

export const PHPVersionsPage = () => {
  const [active, setActive] = useState<TabKey>("versions");

  return (
    <div style={{ padding: 24 }}>
      <Typography.Title level={3} style={{ marginTop: 0 }}>
        <ThunderboltOutlined style={{ marginRight: 8 }} />
        PHP Manager
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        Install PHP versions and manage extensions per version.
      </Typography.Paragraph>

      {/* Card.tabList pins the tab strip to the top-left of the card body —
          mirrors the Server Settings page for consistency across admin pages. */}
      <Card
        tabList={[
          {
            key: "versions",
            tab: (
              <span>
                <CodeOutlined /> PHP Versions
              </span>
            ),
          },
          {
            key: "extensions",
            tab: (
              <span>
                <ApiOutlined /> PHP Extensions
              </span>
            ),
          },
        ]}
        activeTabKey={active}
        onTabChange={(k) => setActive(k as TabKey)}
      >
        {active === "versions" ? <VersionsTab /> : <PHPExtensionsTab />}
      </Card>
    </div>
  );
};
