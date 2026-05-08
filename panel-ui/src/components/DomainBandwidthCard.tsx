// DomainBandwidthCard — per-domain bandwidth card with prior-30-day
// total + sparkline + request count (M13.1).
//
// Mounts on both the admin DomainEdit and the user domain detail
// pages. Pulls /domains/:id/bandwidth (the M13.1 endpoint that
// reads bw_daily). Self-scoping — no-op render with a friendly
// "no data yet" line until the daily goaccess scan populates the
// table.
import { Card, Skeleton, Space, Statistic, Typography } from "antd";
import { useQuery } from "@tanstack/react-query";

import { apiClient } from "../apiClient";
import { humanBytes } from "../utils/bytes";
import { Sparkline } from "./Sparkline";

interface BandwidthResponse {
  domain_id: string;
  from: string;
  to: string;
  bytes_total: number;
  requests_total: number;
  daily: { day: string; bytes_total: number; requests_total: number }[];
}

export interface DomainBandwidthCardProps {
  domainId: string;
}

export function DomainBandwidthCard({ domainId }: DomainBandwidthCardProps) {
  const q = useQuery<BandwidthResponse>({
    queryKey: ["domain-bandwidth", domainId],
    queryFn: async () => {
      const { data } = await apiClient.get<BandwidthResponse>(
        `/domains/${domainId}/bandwidth`,
      );
      return data;
    },
    refetchInterval: 5 * 60 * 1000,
  });

  if (q.isLoading) {
    return (
      <Card title="Bandwidth (last 30 days)">
        <Skeleton active paragraph={{ rows: 2 }} />
      </Card>
    );
  }

  if (q.error || !q.data) {
    return (
      <Card title="Bandwidth (last 30 days)">
        <Typography.Text type="secondary">
          No bandwidth data yet — the daily goaccess scan runs at 00:30 UTC.
        </Typography.Text>
      </Card>
    );
  }

  const series = q.data.daily.map((p) => ({ x: p.day, y: p.bytes_total }));

  return (
    <Card title="Bandwidth (last 30 days)">
      <Space size="large" align="start" wrap>
        <Statistic title="Total" value={humanBytes(q.data.bytes_total)} />
        <Statistic title="Requests" value={q.data.requests_total.toLocaleString()} />
        <div>
          <Typography.Text type="secondary" style={{ display: "block", marginBottom: 4 }}>
            Daily series
          </Typography.Text>
          <Sparkline data={series} width={260} height={48} formatY={humanBytes} />
        </div>
      </Space>
    </Card>
  );
}
