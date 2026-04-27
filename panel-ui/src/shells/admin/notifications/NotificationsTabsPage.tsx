// NotificationsTabsPage — unified admin Notifications page with tabs
// for Channels + History. Matches MailTabsPage (Card.tabList pattern).
import { Card, Space, Typography } from "antd";
import { useNavigate, useParams } from "react-router";

import { BellOutlined } from "@icons";

import { ChannelsTab } from "./ChannelsTab";
import { EventsTab } from "./EventsTab";
import { HistoryTab } from "./HistoryTab";
import { WebPushTab } from "./WebPushTab";

const TAB_KEYS = ["channels", "events", "webpush", "history"] as const;
type TabKey = (typeof TAB_KEYS)[number];
const DEFAULT_TAB: TabKey = "channels";

const TAB_LABELS: Record<TabKey, string> = {
  channels: "Channels",
  events: "Events",
  webpush: "Web Push",
  history: "History",
};

export const NotificationsTabsPage = () => {
  const { tab } = useParams<{ tab?: string }>();
  const navigate = useNavigate();
  const activeKey: TabKey = (TAB_KEYS as readonly string[]).includes(tab ?? "")
    ? (tab as TabKey)
    : DEFAULT_TAB;

  const renderTab = () => {
    switch (activeKey) {
      case "channels":
        return <ChannelsTab />;
      case "events":
        return <EventsTab />;
      case "webpush":
        return <WebPushTab />;
      case "history":
        return <HistoryTab />;
    }
  };

  return (
    <div>
      <Space
        wrap
        align="center"
        style={{ marginBottom: 16, width: "100%", justifyContent: "space-between" }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          <BellOutlined /> Notifications
        </Typography.Title>
      </Space>

      <Card
        tabList={TAB_KEYS.map((k) => ({ key: k, tab: TAB_LABELS[k] }))}
        activeTabKey={activeKey}
        onTabChange={(k) => navigate(`/jabali-admin/notifications/${k}`)}
      >
        {renderTab()}
      </Card>
    </div>
  );
};
