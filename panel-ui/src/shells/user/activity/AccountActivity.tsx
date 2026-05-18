// AccountActivity — the user's own "Account Activity" feed
// (M49 / ADR-0106). Compromise-detection surface: the user can spot
// an SSH key / login / change they did not make.
//
// Read-only. Backed by GET /api/v1/me/activity — the subject scope is
// enforced SERVER-SIDE in the repo (never a client filter), so this
// only ever shows the caller's own rows. Same useTableURL +
// SearchableTableStringQ shape as the admin view, friendlier columns.
import { Card, Space, Table, Tag, Typography } from "antd";
import { FileTextOutlined } from "@icons";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useTableURL } from "../../../hooks/useTableURL";

type Activity = {
  id: string;
  ts: string;
  actor_kind: string;
  action: string;
  target_type: string;
  target_id: string;
  result: "ok" | "denied" | "error";
  source_ip?: string;
};

const resultTag = (r: Activity["result"]) => (
  <Tag color={r === "ok" ? "green" : r === "denied" ? "gold" : "red"}>{r}</Tag>
);

const fmtTS = (ts: string) => {
  const d = new Date(ts);
  return Number.isNaN(d.getTime())
    ? ts
    : d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
};

const dash = <Typography.Text type="secondary">—</Typography.Text>;

export const AccountActivity = () => {
  const query = useTableURL<Activity>({
    resource: "me/activity",
    defaultSort: "ts",
    defaultOrder: "desc",
  });

  return (
    <div>
      <Space
        wrap
        align="center"
        style={{ marginBottom: 16, width: "100%", justifyContent: "space-between" }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          <FileTextOutlined /> Account Activity
        </Typography.Title>
      </Space>

      <Card>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          Recent activity on your account. If you see something you did
          not do, change your password and contact your administrator.
        </Typography.Paragraph>
        <SearchableTableStringQ<Activity>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search activity"
          onSearchChange={(q) => query.setParams({ q, page: 1 })}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
          }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column
            dataIndex="ts"
            title="When (UTC)"
            key="ts"
            render={(ts: string) => <code>{fmtTS(ts)}</code>}
          />
          <Table.Column
            dataIndex="action"
            title="What"
            key="action"
            render={(a: string, r: Activity) => (
              <span>
                {r.actor_kind === "admin" ? (
                  <Tag color="blue">by admin</Tag>
                ) : null}
                <code>{a}</code>
              </span>
            )}
          />
          <Table.Column
            title="Target"
            key="target"
            render={(_: unknown, r: Activity) =>
              r.target_type || r.target_id ? (
                <code>
                  {r.target_type}/{r.target_id}
                </code>
              ) : (
                dash
              )
            }
          />
          <Table.Column
            dataIndex="result"
            title="Result"
            key="result"
            render={resultTag}
          />
          <Table.Column
            dataIndex="source_ip"
            title="Source IP"
            key="source_ip"
            render={(ip?: string) => (ip ? <code>{ip}</code> : dash)}
          />
        </SearchableTableStringQ>
      </Card>
    </div>
  );
};
