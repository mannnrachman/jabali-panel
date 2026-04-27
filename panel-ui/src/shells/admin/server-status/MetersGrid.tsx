// MetersGrid — individual CPU/Memory/Swap/Load meter cards.
//
// Each meter is a standalone <Card>; ServerStatusPage drops them into
// the Masonry layout as individual children so they flow with every
// other card on the page rather than monopolising a full row.
import { Card, Progress, Space, Tooltip, Typography } from "antd";

import type { CPUSlice, HostSlice } from "../../../hooks/useServerStatus";

const usageColor = (pct: number): string => {
  if (pct >= 90) return "#cf1322";
  if (pct >= 70) return "#fa8c16";
  return "#52c41a";
};

interface MeterProps {
  host: HostSlice | null;
  cpu: CPUSlice | null;
}

// CPUMeterCard renders CPU usage + 1/5/15m load averages in a single
// card. Same physical resource (the CPU); reading them together is the
// natural diagnostic flow ("usage spiking" + "load avg trend").
export function CPUMeterCard({ host, cpu }: MeterProps) {
  const cpuPct = cpu?.usage_percent ?? 0;
  const cpuWarming = cpu?.warming_up ?? true;
  const cores = host?.cpu_count ?? 1;
  const load1 = host?.load_avg?.[0] ?? 0;
  const load5 = host?.load_avg?.[1] ?? 0;
  const load15 = host?.load_avg?.[2] ?? 0;
  return (
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
      <div style={{ marginTop: 12 }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Load average
        </Typography.Text>
        <div style={{ marginTop: 4 }}>
          <Space size={16}>
            <LoadStat label="1m" value={load1} cores={cores} />
            <LoadStat label="5m" value={load5} cores={cores} />
            <LoadStat label="15m" value={load15} cores={cores} />
          </Space>
        </div>
      </div>
    </Card>
  );
}

// MemoryMeterCard renders Memory + Swap stacked in a single card.
// Same physical resource family (RAM + paged-out RAM); operators
// already read them together when diagnosing pressure. Combining
// halves the masonry slot count without losing any data.
export function MemoryMeterCard({ host }: MeterProps) {
  const memUsedPct =
    host && host.mem_total_kb > 0
      ? (host.mem_used_kb / host.mem_total_kb) * 100
      : 0;
  const hasSwap = !!host && host.swap_total_kb > 0;
  const swapUsedPct = hasSwap ? (host!.swap_used_kb / host!.swap_total_kb) * 100 : 0;
  return (
    <Card title="Memory" size="small">
      <div>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          RAM
        </Typography.Text>
        <Progress
          percent={Math.round(memUsedPct)}
          strokeColor={usageColor(memUsedPct)}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {humanKB(host?.mem_used_kb ?? 0)} / {humanKB(host?.mem_total_kb ?? 0)}
        </Typography.Text>
      </div>
      <div style={{ marginTop: 12 }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Swap
        </Typography.Text>
        {hasSwap ? (
          <>
            <Progress
              percent={Math.round(swapUsedPct)}
              strokeColor={usageColor(swapUsedPct)}
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {humanKB(host!.swap_used_kb)} / {humanKB(host!.swap_total_kb)}
            </Typography.Text>
          </>
        ) : (
          <div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              No swap configured
            </Typography.Text>
          </div>
        )}
      </div>
    </Card>
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
