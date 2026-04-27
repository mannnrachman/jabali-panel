// Plain "used / limit" disk-usage cell for the admin Users list.
// No badge, no tooltip — operator scans a flat column. Polls the same
// /users/:id/usage endpoint as the other per-user cards on a 5s cadence
// with a hashed startup offset so a 20-row page doesn't fire a thundering
// herd.
import { useQuery } from "@tanstack/react-query";
import { Typography } from "antd";
import { useMemo } from "react";

import { apiClient } from "../../../apiClient";

type UsageResponse = {
  effective?: { DiskQuotaMB?: number };
  current?: { disk?: { used_kb?: number; limit_kb?: number } };
};

const REFRESH_MS = 5_000;
const STALE_MS = 4_000;

function hashMod(s: string, n: number): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  return Math.abs(h) % n;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0";
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(0)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

export function UserDiskUsage({ userId }: { userId: string }) {
  const initialDelayMs = useMemo(() => hashMod(userId, REFRESH_MS), [userId]);

  const { data, isLoading } = useQuery({
    queryKey: ["user-usage", userId],
    queryFn: async () => {
      if (initialDelayMs > 0) {
        await new Promise((r) => setTimeout(r, initialDelayMs));
      }
      const resp = await apiClient.get<UsageResponse>(`/users/${userId}/usage`);
      return resp.data;
    },
    refetchInterval: REFRESH_MS,
    staleTime: STALE_MS,
  });

  if (isLoading && !data) {
    return <Typography.Text type="secondary">…</Typography.Text>;
  }

  const usedKB = data?.current?.disk?.used_kb ?? 0;
  // 0 = "no quota plumbing" (e.g. quotacheck couldn't run on busy /).
  // Fall through to effective.DiskQuotaMB so the package limit renders.
  const reportedLimitKB = data?.current?.disk?.limit_kb ?? 0;
  const limitKB =
    reportedLimitKB > 0
      ? reportedLimitKB
      : data?.effective?.DiskQuotaMB
        ? data.effective.DiskQuotaMB * 1024
        : 0;

  if (limitKB > 0) {
    return <span>{`${formatBytes(usedKB * 1024)} / ${formatBytes(limitKB * 1024)}`}</span>;
  }
  if (usedKB > 0) {
    return <span>{formatBytes(usedKB * 1024)}</span>;
  }
  return <Typography.Text type="secondary">—</Typography.Text>;
}
