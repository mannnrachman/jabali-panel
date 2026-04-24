// NotificationBell — topbar unread-count bell + dropdown for M14
// Step 7. Uses TanStack Query polling (30s) so the bell updates even
// when Web Push isn't subscribed (belt + braces per the plan).
import { Badge, Button, Card, Dropdown, Empty, List, Space, Tag, Typography, message } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
    refetchInterval: 30_000,
    staleTime: 10_000,
  });

  const unread = inbox.data?.unread ?? 0;
  const rows = inbox.data?.data ?? [];

  const markAllRead = async () => {
    try {
      await apiClient.post("/notifications/inbox/read-all");
      qc.invalidateQueries({ queryKey: INBOX_KEY });
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Mark all failed");
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
      size="small"
      title="Notifications"
      extra={
        <Button type="link" size="small" onClick={markAllRead} disabled={unread === 0}>
          Mark all read
        </Button>
      }
      actions={[pushToggle]}
      styles={{ body: { padding: 0, maxHeight: 400, overflowY: "auto" } }}
      style={{ width: 360, maxWidth: "100vw" }}
    >
      {rows.length === 0 ? (
        <Empty
          description={inbox.isLoading ? "Loading…" : "No notifications"}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
      ) : (
        <List<NotificationRow>
          itemLayout="horizontal"
          dataSource={rows}
          renderItem={(row) => (
            <List.Item onClick={() => handleItemClick(row)}>
              <List.Item.Meta
                style={{ padding: "0 16px" }}
                title={
                  <Space style={{ width: "100%", justifyContent: "space-between" }}>
                    <Typography.Text>{row.title}</Typography.Text>
                    <Tag color={severityColor[row.severity] ?? "default"}>
                      {row.severity}
                    </Tag>
                  </Space>
                }
                description={
                  <Space direction="vertical" size={0} style={{ width: "100%" }}>
                    <Typography.Paragraph
                      type="secondary"
                      style={{ margin: 0, whiteSpace: "pre-wrap" }}
                      ellipsis={{ rows: 2 }}
                    >
                      {row.body}
                    </Typography.Paragraph>
                    <Typography.Text type="secondary">
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
