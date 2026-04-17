// Per-user systemd slice status badge (step 8 of per-user-slices).
//
// Renders as a compact cell in the admin Users list. Polls the
// /admin/users/:id/slice-status endpoint every 5s while the component
// is mounted; stops automatically when the user navigates away.
//
// Rendering rules:
//   - admin with no Linux username     → "—"
//   - user with no slice / fpm down    → red "down" tag
//   - user with healthy slice          → green tag with Memory · Tasks
//   - loading                          → subdued "…" placeholder
//
// Throttling: we deliberately stagger polls by hashing the user id into
// a 0-4s offset so loading a page of 20 users doesn't produce a 20x
// thundering herd every 5 seconds. Each row starts at a different
// moment inside the 5s cycle, smoothing agent load.
import { useQuery } from "@tanstack/react-query";
import { Tag, Tooltip } from "antd";
import { useMemo } from "react";
import { apiClient } from "../../../apiClient";

type SliceStatus = {
  username: string;
  slice_active: boolean;
  fpm_active: boolean;
  memory_current_bytes: number;
  tasks_current: number;
  cpu_usage_nsec: number;
};

const REFRESH_MS = 5_000;
const STALE_MS = 4_000;

// hash a string → integer in [0, n)
function hashMod(s: string, n: number): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  return Math.abs(h) % n;
}

function formatMemory(bytes: number): string {
  if (bytes <= 0) return "0";
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(0)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

export function UserSliceStatus({ userId }: { userId: string }) {
  // Staggered initial delay so 20 rows don't all fire at t=0. Computed
  // once per userId and stable across re-renders.
  const initialDelayMs = useMemo(() => hashMod(userId, REFRESH_MS), [userId]);

  const { data, isLoading, isError } = useQuery({
    queryKey: ["slice-status", userId],
    queryFn: async () => {
      // Spread initial fetches across the polling window.
      if (initialDelayMs > 0) {
        await new Promise((r) => setTimeout(r, initialDelayMs));
      }
      const resp = await apiClient.get<SliceStatus>(
        `/admin/users/${userId}/slice-status`,
      );
      return resp.data;
    },
    refetchInterval: REFRESH_MS,
    staleTime: STALE_MS,
  });

  if (isLoading && !data) {
    return <span style={{ color: "#999" }}>…</span>;
  }
  if (isError) {
    return (
      <Tooltip title="Failed to fetch slice status">
        <Tag color="default">?</Tag>
      </Tooltip>
    );
  }
  if (!data || data.username === "") {
    // Admin with no Linux user, or server returned an empty username.
    return <span style={{ color: "#999" }}>—</span>;
  }
  if (!data.slice_active && !data.fpm_active) {
    return (
      <Tooltip title="Slice and FPM both inactive">
        <Tag color="red">down</Tag>
      </Tooltip>
    );
  }
  if (!data.fpm_active) {
    return (
      <Tooltip title={`Slice active but jabali-fpm@${data.username} is not`}>
        <Tag color="orange">FPM down</Tag>
      </Tooltip>
    );
  }

  const mem = formatMemory(data.memory_current_bytes);
  return (
    <Tooltip
      title={
        <>
          Memory: {mem}
          <br />
          Tasks: {data.tasks_current}
        </>
      }
    >
      <Tag color="green">
        {mem} · {data.tasks_current} tasks
      </Tag>
    </Tooltip>
  );
}
