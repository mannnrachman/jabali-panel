import { Button, Card, Space, Tag, Typography } from "antd";

import { CheckCircleOutlined, ExclamationCircleOutlined, ReloadOutlined } from "@icons";

import type { HostSlice } from "../../../hooks/useServerStatus";

interface Props {
  host: HostSlice | null;
  asOf: string;
  onRefresh: () => void;
  isFetching: boolean;
}

export function HostHeaderCard({ host, asOf, onRefresh, isFetching }: Props) {
  const uptime = humanizeUptime(host?.uptime_seconds ?? 0);
  const updated = humanizeAgo(asOf);
  return (
    <Card>
      <div style={{ display: "flex", justifyContent: "space-between", flexWrap: "wrap", gap: 12 }}>
        <Space direction="vertical" size={2}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {host?.hostname ?? "—"}
          </Typography.Title>
          <Typography.Text type="secondary">
            {host?.os || "—"} · kernel {host?.kernel || "—"} · {host?.cpu_model || "—"}
          </Typography.Text>
          <Typography.Text type="secondary">
            Uptime {uptime} · TZ {host?.timezone || "—"}
          </Typography.Text>
        </Space>
        <Space direction="vertical" size={4} align="end">
          {host?.ntp_synced ? (
            <Tag color="green" icon={<CheckCircleOutlined />}>NTP synced</Tag>
          ) : (
            <Tag color="orange" icon={<ExclamationCircleOutlined />}>NTP unsynced</Tag>
          )}
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Updated {updated}
          </Typography.Text>
          <Button size="small" icon={<ReloadOutlined />} onClick={onRefresh} loading={isFetching}>
            Refresh
          </Button>
        </Space>
      </div>
    </Card>
  );
}

function humanizeUptime(secs: number): string {
  if (!secs) return "—";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function humanizeAgo(iso: string): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "—";
  const ago = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (ago < 5) return "just now";
  if (ago < 60) return `${ago}s ago`;
  return `${Math.round(ago / 60)}m ago`;
}
