// MyProfileUsageCard — M18 live resource usage for the signed-in user.
//
// Hits GET /api/v1/users/:id/usage which returns { effective, current }.
// effective is the resolved limits (always present), current is the agent's
// live report (may be absent if the agent is down or the slice has no
// running processes). Refreshes on 10s polling — adequate for a user
// self-view; a websocket stream would be overkill.
import { Card, Descriptions, Progress, Space, Typography } from "antd";
import { useEffect, useState } from "react";

import { apiClient } from "../../apiClient";

type Effective = {
  DiskQuotaMB: number;
  CPUQuotaPercent: number;
  MemoryLimitMB: number;
  IOReadMbps: number;
  IOWriteMbps: number;
  MaxTasks: number;
};

type Current = {
  disk?: { used_kb: number; limit_kb: number };
  memory?: { current_bytes: number; max_bytes: number };
  cpu?: { usage_nsec: number; quota_percent: number };
  tasks?: { current: number; max: number };
  io?: { read_bytes: number; write_bytes: number };
};

type UsageResponse = {
  user_id: string;
  effective: Effective;
  current?: Current;
};

export function MyProfileUsageCard({ userId }: { userId: string }) {
  const [data, setData] = useState<UsageResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const fetch = async () => {
      try {
        const resp = await apiClient.get<UsageResponse>(`/users/${userId}/usage`);
        if (alive) {
          setData(resp.data);
          setError(null);
        }
      } catch (err) {
        if (alive) {
          setError(
            (err as { response?: { data?: { error?: string } } }).response?.data
              ?.error ?? "unavailable",
          );
        }
      }
    };
    fetch();
    // 10s poll. Cheaper than websockets and the agent's cgroup reads
    // are already live (memory.current updates on every allocation),
    // so 10s feels instantaneous to a user watching.
    const id = setInterval(fetch, 10_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [userId]);

  if (error) {
    return (
      <Card title="Resource usage" size="small">
        <Typography.Text type="secondary">
          Usage data is currently unavailable.
        </Typography.Text>
      </Card>
    );
  }

  if (!data) {
    return <Card title="Resource usage" size="small" loading />;
  }

  const { effective, current } = data;
  return (
    <Card title="Resource usage" size="small">
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <UsageRow
          label="Disk"
          used={current?.disk?.used_kb ?? 0}
          limit={current?.disk?.limit_kb ?? effective.DiskQuotaMB * 1024}
          formatter={(kb) => humanBytes(kb * 1024)}
        />
        <UsageRow
          label="Memory"
          used={current?.memory?.current_bytes ?? 0}
          limit={
            current?.memory?.max_bytes ??
            effective.MemoryLimitMB * 1024 * 1024
          }
          formatter={humanBytes}
        />
        <Descriptions column={1} size="small">
          <Descriptions.Item label="CPU quota">
            {effective.CPUQuotaPercent > 0
              ? `${effective.CPUQuotaPercent}% (${(effective.CPUQuotaPercent / 100).toFixed(1)} cores)`
              : "unlimited"}
          </Descriptions.Item>
          <Descriptions.Item label="Processes">
            {current?.tasks
              ? `${current.tasks.current} / ${current.tasks.max || "∞"}`
              : effective.MaxTasks > 0
                ? `limit ${effective.MaxTasks}`
                : "unlimited"}
          </Descriptions.Item>
          <Descriptions.Item label="I/O read limit">
            {effective.IOReadMbps > 0
              ? `${effective.IOReadMbps} MB/s`
              : "unlimited"}
          </Descriptions.Item>
          <Descriptions.Item label="I/O write limit">
            {effective.IOWriteMbps > 0
              ? `${effective.IOWriteMbps} MB/s`
              : "unlimited"}
          </Descriptions.Item>
        </Descriptions>
      </Space>
    </Card>
  );
}

function UsageRow({
  label,
  used,
  limit,
  formatter,
}: {
  label: string;
  used: number;
  limit: number;
  formatter: (v: number) => string;
}) {
  // Limit of 0 means unlimited — no bar, just show usage.
  if (limit <= 0) {
    return (
      <div>
        <Typography.Text strong>{label}</Typography.Text>
        <div style={{ display: "flex", justifyContent: "space-between" }}>
          <Typography.Text>{formatter(used)}</Typography.Text>
          <Typography.Text type="secondary">unlimited</Typography.Text>
        </div>
      </div>
    );
  }
  const pct = Math.min(100, Math.round((used / limit) * 100));
  const status = pct >= 95 ? "exception" : pct >= 80 ? "active" : "normal";
  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between" }}>
        <Typography.Text strong>{label}</Typography.Text>
        <Typography.Text type="secondary">
          {formatter(used)} / {formatter(limit)}
        </Typography.Text>
      </div>
      <Progress percent={pct} status={status} showInfo={false} />
    </div>
  );
}

function humanBytes(b: number): string {
  if (b <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let n = b;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return i === 0 ? `${Math.floor(n)} B` : `${n.toFixed(1)} ${units[i]}`;
}
