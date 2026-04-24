// NotificationBell — topbar unread-count bell + dropdown for M14
// Step 7. Uses TanStack Query polling (30s) so the bell updates even
// when Web Push isn't subscribed (belt + braces per the plan).
import { Badge, Button, Card, Dropdown, Empty, List, Popconfirm, Space, Tag, Typography, message } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useNavigate } from "react-router";

import { BellOutlined } from "@icons";

import { apiClient } from "../apiClient";
import { useWebPushSubscription } from "../hooks/useWebPushSubscription";

type NotificationRow = {
  id: string;
  event_kind: string;
  severity: "info" | "warning" | "error" | "critical";
  title: string;
  body: string;
  deeplink?: string;
  created_at: string;
  read_at?: string | null;
};

type InboxResponse = {
  data: NotificationRow[];
  total: number;
  page: number;
  page_size: number;
  unread: number;
  unread_only: boolean;
};

const INBOX_KEY = ["notifications", "inbox"] as const;

const severityColor: Record<NotificationRow["severity"], string> = {
  info: "blue",
  warning: "gold",
  error: "red",
  critical: "magenta",
};

// relativeTime — small no-dep helper. Keeps the bundle thin compared to
// pulling dayjs/relativeTime plugin for a single consumer.
function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const diff = Date.now() - then;
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

export function NotificationBell() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const webpush = useWebPushSubscription();

  const inbox = useQuery<InboxResponse>({
    queryKey: INBOX_KEY,
    queryFn: async () => {
      const { data } = await apiClient.get<InboxResponse>(
        "/notifications/inbox?page_size=10",
      );
      return data;
    },
    // 10s poll is our fallback; the service-worker postMessage below
    // drives instant updates when Web Push is active, and
    // refetchOnWindowFocus picks up anything missed while the tab
    // was backgrounded.
    refetchInterval: 10_000,
    staleTime: 5_000,
    refetchOnWindowFocus: true,
  });

  // Service worker → client bridge: when a Web Push payload arrives
  // (sw-push.js push handler), it posts a `jabali/notification`
  // message to every open tab. Invalidating both the bell query and
  // the admin History tab's query key keeps every surface current.
  useEffect(() => {
    if (typeof navigator === "undefined" || !navigator.serviceWorker) return;
    const onMessage = (event: MessageEvent) => {
      if (event.data && event.data.type === "jabali/notification") {
        qc.invalidateQueries({ queryKey: ["notifications"] });
      }
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => {
      navigator.serviceWorker.removeEventListener("message", onMessage);
    };
  }, [qc]);

  const unread = inbox.data?.unread ?? 0;
  const rows = inbox.data?.data ?? [];

  const markAllRead = async () => {
    try {
      await apiClient.post("/notifications/inbox/read-all");
      qc.invalidateQueries({ queryKey: ["notifications"] });
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Mark all failed");
    }
  };

  const clearAll = async () => {
    try {
      await apiClient.delete("/notifications/inbox");
      qc.invalidateQueries({ queryKey: ["notifications"] });
      message.success("All notifications cleared");
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Clear failed");
    }
  };

  const handleItemClick = async (row: NotificationRow) => {
    try {
      if (!row.read_at) {
        await apiClient.post(`/notifications/inbox/${row.id}/read`);
        qc.invalidateQueries({ queryKey: INBOX_KEY });
      }
    } catch {
      // Silent — clicking through shouldn't block navigation.
    }
    if (row.deeplink) {
      navigate(row.deeplink);
    }
  };

  const pushToggle = (() => {
    if (!webpush.supported) {
      return (
        <Typography.Text type="secondary">Browser push not supported</Typography.Text>
      );
    }
    if (webpush.permission === "denied") {
      return (
        <Typography.Text type="secondary">
          Push blocked — enable in your browser settings
        </Typography.Text>
      );
    }
    if (webpush.subscribed) {
      return (
        <Button size="small" onClick={() => void webpush.unsubscribe()} loading={webpush.loading}>
          Disable browser push
        </Button>
      );
    }
    return (
      <Button size="small" type="primary" onClick={() => void webpush.subscribe()} loading={webpush.loading}>
        Enable browser push
      </Button>
    );
  })();

  const content = (
    <Card
      title={
        <Space size={6}>
          <BellOutlined />
          <Typography.Text strong>Notifications</Typography.Text>
          {unread > 0 && <Badge count={unread} size="small" />}
        </Space>
      }
      extra={
        <Space size={4}>
          <Button size="small" onClick={markAllRead} disabled={unread === 0}>
            Mark all read
          </Button>
          <Popconfirm
            title="Clear all notifications?"
            description="This deletes every notification in your inbox."
            onConfirm={clearAll}
            okText="Clear"
            okButtonProps={{ danger: true }}
          >
            <Button size="small" danger disabled={rows.length === 0}>
              Clear
            </Button>
          </Popconfirm>
        </Space>
      }
      actions={[pushToggle]}
      styles={{ body: { padding: 0, maxHeight: 420, overflowY: "auto" } }}
      style={{ width: 380, maxWidth: "100vw" }}
    >
      {rows.length === 0 ? (
        <Empty
          description={inbox.isLoading ? "Loading…" : "No notifications"}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          style={{ padding: 24 }}
        />
      ) : (
        <List<NotificationRow>
          itemLayout="horizontal"
          dataSource={rows}
          renderItem={(row) => (
            <List.Item
              onClick={() => handleItemClick(row)}
              style={{
                padding: "12px 16px",
                cursor: row.deeplink ? "pointer" : "default",
                background: row.read_at ? undefined : "var(--ant-color-primary-bg, rgba(22,119,255,0.06))",
              }}
            >
              <List.Item.Meta
                avatar={<Tag color={severityColor[row.severity] ?? "default"} style={{ marginInlineEnd: 0 }}>{row.severity}</Tag>}
                title={<Typography.Text strong>{row.title}</Typography.Text>}
                description={
                  <Space direction="vertical" size={2} style={{ width: "100%" }}>
                    <Typography.Paragraph
                      type="secondary"
                      style={{ margin: 0, whiteSpace: "pre-wrap", fontSize: 12 }}
                      ellipsis={{ rows: 2 }}
                    >
                      {row.body}
                    </Typography.Paragraph>
                    <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                      {relativeTime(row.created_at)}
                    </Typography.Text>
                  </Space>
                }
              />
            </List.Item>
          )}
        />
      )}
    </Card>
  );

  return (
    <Dropdown
      dropdownRender={() => content}
      trigger={["click"]}
      placement="bottomRight"
    >
      <Button type="text" aria-label="Notifications">
        <Badge count={unread} size="small" overflowCount={99}>
          <BellOutlined />
        </Badge>
      </Button>
    </Dropdown>
  );
}
