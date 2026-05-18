// AdminAuditList — admin "Audit Log" forensics view (M49 / ADR-0106).
//
// Read-only: same useTableURL + SearchableTableStringQ shape as
// AdminIPList, minus the create/edit/delete surface. Backed by
// GET /api/v1/admin/audit (RequireAdmin). The list envelope
// {data,total,page,page_size} is read via query.items/query.total.
import { Card, Space, Table, Tag, Typography } from "antd";
import { SafetyOutlined } from "@icons";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useTableURL } from "../../../hooks/useTableURL";

type AuditEvent = {
  id: string;
  ts: string;
  actor_user_id?: string;
  actor_kind: string;
  subject_user_id?: string;
  action: string;
  target_type: string;
  target_id: string;
  result: "ok" | "denied" | "error";
  source_ip?: string;
  request_id?: string;
};

const resultTag = (r: AuditEvent["result"]) => (
  <Tag color={r === "ok" ? "green" : r === "denied" ? "gold" : "red"}>{r}</Tag>
);

const fmtTS = (ts: string) => {
  const d = new Date(ts);
  return Number.isNaN(d.getTime())
    ? ts
    : d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
};

const dash = <Typography.Text type="secondary">—</Typography.Text>;

export const AdminAuditList = () => {
  const query = useTableURL<AuditEvent>({
    resource: "admin/audit",
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
          <SafetyOutlined /> Audit Log
        </Typography.Title>
      </Space>

      <Card>
        <SearchableTableStringQ<AuditEvent>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search action / target / actor / result"
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
            title="Actor"
            key="actor"
            render={(_: unknown, r: AuditEvent) => (
              <span>
                <Tag>{r.actor_kind}</Tag>
                {r.actor_user_id ? <code>{r.actor_user_id}</code> : null}
              </span>
            )}
          />
          <Table.Column
            dataIndex="action"
            title="Action"
            key="action"
            render={(a: string) => <code>{a}</code>}
          />
          <Table.Column
            title="Target"
            key="target"
            render={(_: unknown, r: AuditEvent) =>
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
