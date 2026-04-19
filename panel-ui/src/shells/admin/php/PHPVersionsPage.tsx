import { useState } from "react";
import { Tabs } from "antd";
import { ApiOutlined, CodeOutlined } from "@ant-design/icons";
import { VersionsTab } from "./VersionsTab";
import { PHPExtensionsTab } from "./PHPExtensionsTab";

type TabKey = "versions" | "extensions";

export const PHPVersionsPage = () => {
  const [active, setActive] = useState<TabKey>("versions");
  return (
    <Tabs
      activeKey={active}
      onChange={(k) => setActive(k as TabKey)}
      centered
      items={[
        {
          key: "versions",
          label: (
            <span>
              <CodeOutlined /> PHP Versions
            </span>
          ),
          children: <VersionsTab />,
        },
        {
          key: "extensions",
          label: (
            <span>
              <ApiOutlined /> PHP Extensions
            </span>
          ),
          children: <PHPExtensionsTab />,
        },
      ]}
    />
  );
};
