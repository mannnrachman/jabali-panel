// HistoryTab — admin Notifications > History table (M14 follow-up).
//
// Paginated table of notification_history rows. Admins see both their
// own per-user deliveries AND system-wide broadcast rows (user_id IS
// NULL) via ListForAdminInbox on the backend. Click a row to mark it
// read and (if a deeplink exists) navigate there.
import { Button, Space, Table, Tag, Tooltip, Typography, message } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";

import { apiClient } from "../../../apiClient";

type NotificationHistoryRow = {
  id: string;
  event_kind: string;
  severity: "info" | "warning" | "error" | "critical";
  title: string;
  body: string;
  deeplink?: string;
  outcome: "pending" | "sent" | "failed" | "skipped";
  channel_id?: string | null;
  error_message?: string;
  retry_count: number;
  read_at?: string | null;
  created_at: string;
};

type InboxListResponse = {
  data: NotificationHistoryRow[];
  total: number;
  page: number;
  page_size: number;
  unread: number;
};

const severityColor: Record<NotificationHistoryRow["severity"], string> = {
  info: "blue",
  warning: "gold",
  error: "red",
  critical: "magenta",
};

const outcomeColor: Record<NotificationHistoryRow["outcome"], string> = {
  pending: "default",
  sent: "green",
  failed: "red",
  skipped: "orange",
};

function formatTs(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export const HistoryTab = () => {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const listKey = ["notifications", "history", page, pageSize] as const;

  const query = useQuery<InboxListResponse>({
    queryKey: listKey,
    queryFn: async () => {
      const { data } = await apiClient.get<InboxListResponse>(
        `/notifications/inbox?page=${page}&page_size=${pageSize}`,
      );
      return data;
    },
    refetchInterval: 30_000,
    staleTime: 10_000,
  });

  const rows = query.data?.data ?? [];
  const total = query.data?.total ?? 0;
  const unread = query.data?.unread ?? 0;

  const markAllRead = async () => {
    try {
      await apiClient.post("/notifications/inbox/read-all");
      qc.invalidateQueries({ queryKey: ["notifications"] });
      message.success("Marked all as read");
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Mark-all failed");
    }
  };

  const markRead = async (row: NotificationHistoryRow) => {
    if (row.read_at) return;
    try {
      await apiClient.post(`/notifications/inbox/${row.id}/read`);
      qc.invalidateQueries({ queryKey: ["notifications"] });
    } catch {
      // Non-blocking.
    }
  };

  const handleRowClick = (row: NotificationHistoryRow) => {
    void markRead(row);
    if (row.deeplink) {
      navigate(row.deeplink);
    }
  };

  return (
    <div>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "space-between" }}>
        <Typography.Text type="secondary">
          {total} total · {unread} unread
        </Typography.Text>
        <Button onClick={markAllRead} disabled={unread === 0}>
          Mark all as read
        </Button>
      </Space>

      <Table<NotificationHistoryRow>
        rowKey="id"
        loading={query.isLoading}
        dataSource={rows}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: true,
          pageSizeOptions: ["10", "25", "50", "100"],
          onChange: (p, s) => {
            setPage(p);
            setPageSize(s);
          },
        }}
        scroll={{ x: "max-content" }}
        onRow={(row) => ({
          onClick: () => handleRowClick(row),
          style: {
            cursor: row.deeplink ? "pointer" : "default",
            background: row.read_at ? undefined : "rgba(24,144,255,0.04)",
          },
        })}
      >
        <Table.Column<NotificationHistoryRow>
          title="Time"
          dataIndex="created_at"
          width={180}
          render={(v: string) => formatTs(v)}
        />
        <Table.Column<NotificationHistoryRow>
          title="Severity"
          dataIndex="severity"
          width={110}
          render={(s: NotificationHistoryRow["severity"]) => (
            <Tag color={severityColor[s] ?? "default"}>{s}</Tag>
          )}
        />
        <Table.Column<NotificationHistoryRow>
          title="Event"
          dataIndex="event_kind"
          width={200}
          render={(k: string) => <code style={{ fontSize: 12 }}>{k}</code>}
        />
        <Table.Column<NotificationHistoryRow>
          title="Title"
          dataIndex="title"
          render={(v: string) => <Typography.Text strong>{v}</Typography.Text>}
        />
        <Table.Column<NotificationHistoryRow>
          title="Body"
          dataIndex="body"
          render={(v: string) => (
            <Typography.Paragraph
              style={{ margin: 0, fontSize: 12, whiteSpace: "pre-wrap" }}
              ellipsis={{ rows: 2, expandable: true, symbol: "more" }}
            >
              {v}
            </Typography.Paragraph>
          )}
        />
        <Table.Column<NotificationHistoryRow>
          title="Outcome"
          dataIndex="outcome"
          width={120}
          render={(o: NotificationHistoryRow["outcome"], row) => {
            const tag = <Tag color={outcomeColor[o] ?? "default"}>{o}</Tag>;
            if (o === "failed" && row.error_message) {
              return <Tooltip title={row.error_message}>{tag}</Tooltip>;
            }
            return tag;
          }}
        />
        <Table.Column<NotificationHistoryRow>
          title="Read"
          dataIndex="read_at"
          width={80}
          render={(v: string | null | undefined) =>
            v ? <Tag color="default">read</Tag> : <Tag color="blue">new</Tag>
          }
        />
      </Table>
    </div>
  );
};
