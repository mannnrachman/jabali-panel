// WebPushTab — admin Notifications > Web Push panel.
//
// Web Push subscriptions are per-browser, not per-channel: VAPID keys
// live in server settings, the actual subscription handle is bound to
// the navigator that's currently logged in. Treating it as a
// notification "channel" was misleading — every browser already needs
// its own enable click anyway. This tab exposes the same toggle the
// bell dropdown footer offers, with a bit more context around it.
import { Alert, Button, Card, Space, Typography, message } from "antd";
import { BellOutlined } from "@icons";

import { useWebPushSubscription } from "../../../hooks/useWebPushSubscription";

export const WebPushTab = () => {
  const webpush = useWebPushSubscription();

  const handleEnable = async () => {
    try {
      await webpush.subscribe();
      message.success("Browser push enabled");
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Subscribe failed");
    }
  };

  const handleDisable = async () => {
    try {
      await webpush.unsubscribe();
      message.success("Browser push disabled");
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Unsubscribe failed");
    }
  };

  const renderControls = () => {
    if (!webpush.supported) {
      return (
        <Alert
          type="warning"
          showIcon
          message="Browser push is not supported in this browser"
          description="Web Push relies on the Service Worker + Push API. Try a modern Chrome, Firefox, Safari 16+, or Edge build."
        />
      );
    }
    if (webpush.permission === "denied") {
      return (
        <Alert
          type="warning"
          showIcon
          message="Browser blocked notifications"
          description="Open the site permissions for this domain in your browser settings and re-allow notifications, then reload."
        />
      );
    }
    if (webpush.subscribed) {
      return (
        <Space direction="vertical" size={12}>
          <Alert
            type="success"
            showIcon
            message="This browser is receiving Jabali push notifications"
            description="Notifications appear via your operating system even when the panel tab isn't focused. Disable any time below."
          />
          <Button danger onClick={handleDisable} loading={webpush.loading}>
            Disable on this browser
          </Button>
        </Space>
      );
    }
    return (
      <Space direction="vertical" size={12}>
        <Typography.Paragraph style={{ marginBottom: 0 }}>
          Web Push delivers notifications to this browser even while the panel
          tab is closed. Each browser needs its own enable click — the
          subscription is tied to the navigator, not your account.
        </Typography.Paragraph>
        <Button type="primary" onClick={handleEnable} loading={webpush.loading}>
          Enable on this browser
        </Button>
      </Space>
    );
  };

  return (
    <div>
      <Card
        title={
          <Space>
            <BellOutlined />
            <span>Web Push</span>
          </Space>
        }
      >
        {renderControls()}
      </Card>
    </div>
  );
};
