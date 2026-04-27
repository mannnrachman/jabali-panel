// QueuesCard — M31.1 dispatcher-queue snapshot for the server-status page.
//
// Reads the `queues` slice from /admin/server-status and renders three
// counters: the M14 notifications stream length, its dead-letter queue,
// and the consumer-group pending list. Numbers refresh on the same 5s
// poll as the rest of the page.
import { Card, Space, Statistic, Typography } from "antd";
import { BellOutlined } from "@icons";

import type { QueuesSlice } from "../../../hooks/useServerStatus";

interface QueuesCardProps {
  queues: QueuesSlice | null;
}

export function QueuesCard({ queues }: QueuesCardProps) {
  if (queues == null) {
    return (
      <Card title="Queues" size="small">
        <Typography.Text type="secondary">
          Redis dispatcher unavailable.
        </Typography.Text>
      </Card>
    );
  }
  return (
    <Card
      title={
        <Space>
          <BellOutlined />
          Notification queues
        </Space>
      }
      size="small"
    >
      <Space size={32} wrap>
        <Statistic
          title="Pending in stream"
          value={queues.notifications_queue}
          valueStyle={
            queues.notifications_queue > 1000
              ? { color: "#cf1322" }
              : undefined
          }
        />
        <Statistic
          title="In flight"
          value={queues.notifications_pending}
        />
        <Statistic
          title="Dead letter"
          value={queues.notifications_dlq}
          valueStyle={
            queues.notifications_dlq > 0 ? { color: "#fa8c16" } : undefined
          }
        />
      </Space>
    </Card>
  );
}
