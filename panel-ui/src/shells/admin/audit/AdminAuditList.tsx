// AdminAuditList — admin "Audit Log" forensics view (M49 / ADR-0106).
//
// Read-only: same useTableURL + SearchableTableStringQ shape as
// AdminIPList, minus the create/edit/delete surface. Backed by
// GET /api/v1/admin/audit (RequireAdmin). The list envelope
// {data,total,page,page_size} is read via query.items/query.total.
//
// Timestamps render in the server timezone (Server Settings →
// Timezone). Actor shows the resolved username (API batch-resolves the
// ULID). The Action cell opens a modal with the full recorded detail.
import { Card, Space, Table, Tag, Typography } from "antd";
import { SafetyOutlined } from "@icons";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import {
  AuditActionLabel,
  AuditDetail,
  type AuditRow,
  dash,
  fmtTSInTz,
  resultTag,
  useServerTz,
} from "../../../components/AuditEventDetail";
import { useTableURL } from "../../../hooks/useTableURL";

export const AdminAuditList = () => {
  const tz = useServerTz();
  const query = useTableURL<AuditRow>({
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
        <SearchableTableStringQ<AuditRow>
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
          expandable={{
            expandedRowRender: (r: AuditRow) => (
              <AuditDetail row={r} tz={tz} />
            ),
          }}
        >
          <Table.Column
            dataIndex="ts"
            title={`When (${tz})`}
            key="ts"
            render={(ts: string) => <code>{fmtTSInTz(ts, tz)}</code>}
          />
          <Table.Column
            title="Actor"
            key="actor"
            render={(_: unknown, r: AuditRow) => (
              <span>
                <Tag>{r.actor_kind}</Tag>
                {r.actor_name ? (
                  r.actor_name
                ) : r.actor_user_id ? (
                  <code>{r.actor_user_id}</code>
                ) : null}
              </span>
            )}
          />
          <Table.Column
            title="Action"
            key="action"
            render={(_: unknown, r: AuditRow) => <AuditActionLabel row={r} />}
          />
          <Table.Column
            title="Target"
            key="target"
            render={(_: unknown, r: AuditRow) =>
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
