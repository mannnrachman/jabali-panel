// UserSlicesCard — per-user cgroup v2 slice metrics (M18 + M31).
// One row per Linux user with a jabali-user-<u>.slice; CPU% is delta-
// computed agent-side over the polling interval, memory.current /
// pids.current read directly. memory.max returning "max" (no limit)
// renders as "—".
import { Card, Table, Tag, Typography } from "antd";

import type { UserSliceMetric, UserSlicesSlice } from "../../../hooks/useServerStatus";

interface Props {
  data: UserSlicesSlice | null;
}

export function UserSlicesCard({ data }: Props) {
  const slices = data?.slices ?? [];
  return (
    <Card title="User slices" size="small">
      {slices.length === 0 ? (
        <Typography.Text type="secondary">
          No per-user slices on this host.
        </Typography.Text>
      ) : (
        <Table<UserSliceMetric>
          rowKey="username"
          size="small"
          dataSource={slices}
          pagination={false}
          scroll={{ x: "max-content" }}
          columns={[
            { title: "User", dataIndex: "username" },
            {
              title: "CPU",
              dataIndex: "cpu_percent",
              render: (v: number) => (
                <Tag color={cpuColor(v)}>{v.toFixed(1)}%</Tag>
              ),
            },
            {
              title: "Memory",
              dataIndex: "memory_bytes",
              render: (v: number, r: UserSliceMetric) =>
                r.memory_max_bytes
                  ? `${humanBytes(v)} / ${humanBytes(r.memory_max_bytes)}`
                  : humanBytes(v),
            },
            {
              title: "Tasks",
              dataIndex: "tasks",
              render: (v: number) => v || "—",
            },
          ]}
        />
      )}
      {data?.warming_up && (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          warming up
        </Typography.Text>
      )}
    </Card>
  );
}

function cpuColor(pct: number): string {
  if (pct >= 90) return "red";
  if (pct >= 70) return "orange";
  return "green";
}

function humanBytes(b: number): string {
  if (!b) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = b;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}
