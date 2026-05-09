// MyProfileUsageCard — M18 live resource usage for the signed-in user.
//
// Hits GET /api/v1/users/:id/usage which returns { effective, current }.
// effective is the resolved limits (always present), current is the agent's
// live report (may be absent if the agent is down or the slice has no
// running processes). Refreshes on 10s polling — adequate for a user
// self-view; a websocket stream would be overkill.
import { Card, Progress, Space, Typography } from "antd";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";

import {
  HddOutlined,
  DatabaseOutlined,
  ThunderboltOutlined,
  AppstoreLayoutOutlined,
  DownloadOutlined,
  UploadOutlined,
} from "@icons";

import { apiClient } from "../../apiClient";
import { humanBytes } from "../../utils/bytes";

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
    const id = setInterval(fetch, 10_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [userId]);

  if (error) {
    return (
      <Card title="Resource usage">
        <Typography.Text type="secondary">
          Usage data is currently unavailable.
        </Typography.Text>
      </Card>
    );
  }

  if (!data) {
    return <Card title="Resource usage" loading />;
  }

  const { effective, current } = data;

  const diskUsedKB = current?.disk?.used_kb ?? 0;
  const diskLimitKB =
    (current?.disk?.limit_kb ?? 0) > 0
      ? (current?.disk?.limit_kb ?? 0)
      : effective.DiskQuotaMB * 1024;
  const memUsedB = current?.memory?.current_bytes ?? 0;
  const memLimitB =
    current?.memory?.max_bytes ?? effective.MemoryLimitMB * 1024 * 1024;

  const cpuValue =
    effective.CPUQuotaPercent > 0
      ? `${effective.CPUQuotaPercent}% (${(effective.CPUQuotaPercent / 100).toFixed(1)} cores)`
      : "Unlimited";
  const procValue = current?.tasks
    ? `${current.tasks.current}${current.tasks.max ? ` / ${current.tasks.max}` : ""}`
    : effective.MaxTasks > 0
      ? `Limit ${effective.MaxTasks}`
      : "Unlimited";
  const ioReadValue =
    effective.IOReadMbps > 0 ? `${effective.IOReadMbps} MB/s` : "Unlimited";
  const ioWriteValue =
    effective.IOWriteMbps > 0 ? `${effective.IOWriteMbps} MB/s` : "Unlimited";

  return (
    <Card title="Resource usage">
      <Space direction="vertical" size={20} style={{ width: "100%" }}>
        <UsageRow
          icon={<HddOutlined />}
          iconBg="rgba(22, 119, 255, 0.12)"
          iconColor="#1677ff"
          label="Disk"
          used={diskUsedKB * 1024}
          limit={diskLimitKB * 1024}
        />
        <UsageRow
          icon={<DatabaseOutlined />}
          iconBg="rgba(146, 84, 222, 0.14)"
          iconColor="#9254de"
          label="Memory"
          used={memUsedB}
          limit={memLimitB}
        />
        <SimpleRow
          icon={<ThunderboltOutlined />}
          iconBg="rgba(82, 196, 26, 0.14)"
          iconColor="#52c41a"
          label="CPU quota"
          value={cpuValue}
        />
        <SimpleRow
          icon={<AppstoreLayoutOutlined />}
          iconBg="rgba(250, 140, 22, 0.14)"
          iconColor="#fa8c16"
          label="Processes"
          value={procValue}
        />
        <SimpleRow
          icon={<DownloadOutlined />}
          iconBg="rgba(19, 194, 194, 0.14)"
          iconColor="#13c2c2"
          label="I/O read"
          value={ioReadValue}
        />
        <SimpleRow
          icon={<UploadOutlined />}
          iconBg="rgba(235, 47, 150, 0.14)"
          iconColor="#eb2f96"
          label="I/O write"
          value={ioWriteValue}
        />
      </Space>
    </Card>
  );
}

interface IconBoxProps {
  icon: ReactNode;
  iconBg: string;
  iconColor: string;
}

const IconBox = ({ icon, iconBg, iconColor }: IconBoxProps) => (
  <div
    style={{
      width: 36,
      height: 36,
      borderRadius: 10,
      background: iconBg,
      color: iconColor,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      fontSize: 16,
      flex: "0 0 36px",
    }}
  >
    {icon}
  </div>
);

interface UsageRowProps extends IconBoxProps {
  label: string;
  used: number;
  limit: number;
}

function UsageRow({ icon, iconBg, iconColor, label, used, limit }: UsageRowProps) {
  const limited = limit > 0;
  const pct = limited ? Math.min(100, Math.round((used / limit) * 100)) : 0;
  const status = pct >= 95 ? "exception" : pct >= 80 ? "active" : "normal";
  return (
    <Space align="start" size={12} style={{ width: "100%" }}>
      <IconBox icon={icon} iconBg={iconBg} iconColor={iconColor} />
      <div style={{ flex: 1, minWidth: 0, width: "100%" }}>
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            gap: 8,
            marginBottom: 4,
          }}
        >
          <Typography.Text strong>{label}</Typography.Text>
          <Typography.Text type="secondary">
            {limited
              ? `${humanBytes(used)} / ${humanBytes(limit)}`
              : `${humanBytes(used)} · Unlimited`}
          </Typography.Text>
        </div>
        {limited && <Progress percent={pct} status={status} showInfo size="small" />}
      </div>
    </Space>
  );
}

interface SimpleRowProps extends IconBoxProps {
  label: string;
  value: string;
}

function SimpleRow({ icon, iconBg, iconColor, label, value }: SimpleRowProps) {
  return (
    <Space align="center" size={12} style={{ width: "100%" }}>
      <IconBox icon={icon} iconBg={iconBg} iconColor={iconColor} />
      <div
        style={{
          flex: 1,
          display: "flex",
          justifyContent: "space-between",
          gap: 8,
        }}
      >
        <Typography.Text strong>{label}</Typography.Text>
        <Typography.Text type="secondary">{value}</Typography.Text>
      </div>
    </Space>
  );
}

