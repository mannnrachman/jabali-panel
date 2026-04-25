// EventsTab — admin Notifications > Events. Per-event-kind enable
// toggle. Defaults seeded by panel-api first-boot per
// models.AllNotificationEventKinds (important = on).
import { Switch, Table, Tag, Tooltip, Typography, message } from "antd";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";

type EventKindRow = {
  kind: string;
  label: string;
  description: string;
  severity: "info" | "warning" | "error" | "critical";
  enabled: boolean;
  default_on: boolean;
};

const LIST_KEY = ["admin", "notification-events"] as const;

const severityColor: Record<EventKindRow["severity"], string> = {
  info: "blue",
  warning: "gold",
  error: "red",
  critical: "magenta",
};

export const EventsTab = () => {
  const qc = useQueryClient();

  const list = useQuery<{ data: EventKindRow[] }>({
    queryKey: LIST_KEY,
    queryFn: async () => {
      const { data } = await apiClient.get<{ data: EventKindRow[] }>(
        "/admin/settings/notification-events",
      );
      return data;
    },
  });

  const toggle = async (row: EventKindRow, next: boolean) => {
    try {
      await apiClient.patch(`/admin/settings/notification-events/${row.kind}`, {
        enabled: next,
      });
      qc.invalidateQueries({ queryKey: LIST_KEY });
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Toggle failed");
    }
  };

  return (
    <Table<EventKindRow>
      rowKey="kind"
      loading={list.isLoading}
      dataSource={list.data?.data ?? []}
      pagination={false}
      scroll={{ x: "max-content" }}
    >
      <Table.Column<EventKindRow>
        title="Event"
        dataIndex="label"
        render={(label: string, row) => (
          <div>
            <Typography.Text strong>{label}</Typography.Text>
            <div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                <code>{row.kind}</code>
              </Typography.Text>
            </div>
          </div>
        )}
      />
      <Table.Column<EventKindRow>
        title="Severity"
        dataIndex="severity"
        width={120}
        render={(s: EventKindRow["severity"]) => (
          <Tag color={severityColor[s] ?? "default"}>{s}</Tag>
        )}
      />
      <Table.Column<EventKindRow>
        title="Description"
        dataIndex="description"
        render={(v: string) => (
          <Typography.Paragraph
            type="secondary"
            style={{ margin: 0, fontSize: 12 }}
            ellipsis={{ rows: 2, expandable: true, symbol: "more" }}
          >
            {v}
          </Typography.Paragraph>
        )}
      />
      <Table.Column<EventKindRow>
        title="Enabled"
        dataIndex="enabled"
        width={120}
        render={(enabled: boolean, row) => (
          <Tooltip
            title={
              row.enabled === row.default_on
                ? row.default_on
                  ? "Default: on"
                  : "Default: off"
                : row.default_on
                  ? "Overridden — default is on"
                  : "Overridden — default is off"
            }
          >
            <Switch checked={enabled} onChange={(next) => toggle(row, next)} />
          </Tooltip>
        )}
      />
    </Table>
  );
};
