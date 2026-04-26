// MetersGrid — 4-up CPU/Memory/Swap/Load with threshold colours.
// Responsive: <md → 1col, ≥md → 2col, ≥lg → 4col.
import { Card, Col, Progress, Row, Space, Tooltip, Typography } from "antd";

import type { CPUSlice, HostSlice } from "../../../hooks/useServerStatus";

interface Props {
  host: HostSlice | null;
  cpu: CPUSlice | null;
}

const usageColor = (pct: number): string => {
  if (pct >= 90) return "#cf1322";
  if (pct >= 70) return "#fa8c16";
  return "#52c41a";
};

export function MetersGrid({ host, cpu }: Props) {
  const memUsedPct =
    host && host.mem_total_kb > 0
      ? (host.mem_used_kb / host.mem_total_kb) * 100
      : 0;
  const swapUsedPct =
    host && host.swap_total_kb > 0
      ? (host.swap_used_kb / host.swap_total_kb) * 100
      : 0;
  const cpuPct = cpu?.usage_percent ?? 0;
  const cpuWarming = cpu?.warming_up ?? true;
  const cores = host?.cpu_count ?? 1;
  const load1 = host?.load_avg?.[0] ?? 0;
  const load5 = host?.load_avg?.[1] ?? 0;
  const load15 = host?.load_avg?.[2] ?? 0;

  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} md={12} lg={6}>
        <Card title="CPU" size="small">
          <Progress
            percent={Math.round(cpuPct)}
            strokeColor={usageColor(cpuPct)}
            status={cpuWarming ? "active" : undefined}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            iowait {cpu?.iowait_percent?.toFixed(1) ?? "0.0"}% · {cores} core
            {cores === 1 ? "" : "s"}
            {cpuWarming ? " · warming up" : ""}
          </Typography.Text>
        </Card>
      </Col>

      <Col xs={24} md={12} lg={6}>
        <Card title="Memory" size="small">
          <Progress
            percent={Math.round(memUsedPct)}
            strokeColor={usageColor(memUsedPct)}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {humanKB(host?.mem_used_kb ?? 0)} / {humanKB(host?.mem_total_kb ?? 0)}
          </Typography.Text>
        </Card>
      </Col>

      {host && host.swap_total_kb > 0 ? (
        <Col xs={24} md={12} lg={6}>
          <Card title="Swap" size="small">
            <Progress
              percent={Math.round(swapUsedPct)}
              strokeColor={usageColor(swapUsedPct)}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {humanKB(host.swap_used_kb)} / {humanKB(host.swap_total_kb)}
            </Typography.Text>
          </Card>
        </Col>
      ) : (
        <Col xs={24} md={12} lg={6}>
          <Card title="Swap" size="small">
            <Typography.Text type="secondary">No swap configured</Typography.Text>
          </Card>
        </Col>
      )}

      <Col xs={24} md={12} lg={6}>
        <Card title="Load" size="small">
          <Space size={12}>
            <LoadStat label="1m" value={load1} cores={cores} />
            <LoadStat label="5m" value={load5} cores={cores} />
            <LoadStat label="15m" value={load15} cores={cores} />
          </Space>
        </Card>
      </Col>
    </Row>
  );
}

function LoadStat({ label, value, cores }: { label: string; value: number; cores: number }) {
  let color = "#52c41a";
  if (value > cores * 2) color = "#cf1322";
  else if (value > cores) color = "#fa8c16";
  return (
    <Tooltip title={`${label} avg`}>
      <div style={{ textAlign: "center" }}>
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>{label}</Typography.Text>
        <div style={{ color, fontWeight: 600, fontSize: 18 }}>{value.toFixed(2)}</div>
      </div>
    </Tooltip>
  );
}

function humanKB(kb: number): string {
  if (!kb) return "0";
  const units = ["KB", "MB", "GB", "TB"];
  let v = kb;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}
