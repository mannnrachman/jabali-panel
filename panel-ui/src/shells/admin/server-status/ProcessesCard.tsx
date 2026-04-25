// ProcessesCard — collapsed by default. Header shows totals; expanding
// renders the top-N RSS table inline.
import { useState } from "react";
import { Button, Card, Space, Statistic, Table } from "antd";

import type { ProcessesSlice, ProcessTop } from "../../../hooks/useServerStatus";

interface Props {
  processes: ProcessesSlice | null;
}

export function ProcessesCard({ processes }: Props) {
  const [expanded, setExpanded] = useState(false);
  const p = processes;

  return (
    <Card
      title="Processes"
      size="small"
      extra={
        <Button size="small" onClick={() => setExpanded((v) => !v)}>
          {expanded ? "Hide top-10" : "Show top-10"}
        </Button>
      }
    >
      <Space size={24} wrap>
        <Statistic title="Total" value={p?.total ?? 0} />
        <Statistic title="Running" value={p?.running ?? 0} />
        <Statistic title="Sleeping" value={p?.sleeping ?? 0} />
        <Statistic
          title="Zombie"
          value={p?.zombie ?? 0}
          valueStyle={{ color: (p?.zombie ?? 0) > 0 ? "#cf1322" : undefined }}
        />
      </Space>
      {expanded && p?.top_by_rss ? (
        <Table<ProcessTop>
          rowKey="pid"
          size="small"
          style={{ marginTop: 12 }}
          dataSource={p.top_by_rss}
          pagination={false}
          scroll={{ x: "max-content" }}
          columns={[
            { title: "PID", dataIndex: "pid", width: 80 },
            { title: "Comm", dataIndex: "comm" },
            { title: "User", dataIndex: "user" },
            {
              title: "RSS",
              dataIndex: "rss_kb",
              render: (kb: number) => humanKB(kb),
            },
            { title: "State", dataIndex: "state", width: 60 },
          ]}
        />
      ) : null}
    </Card>
  );
}

function humanKB(kb: number): string {
  if (!kb) return "0";
  const units = ["KB", "MB", "GB"];
  let v = kb;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}
