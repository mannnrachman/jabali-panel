// useSystemUpdates — TanStack Query wrappers for /api/v1/admin/updates/*.
//
// `check` queries are mutations not queries: they trigger work (apt-get
// update, git fetch) so re-running them is a deliberate operator action,
// not something we want to auto-fire. Status queries refresh on a 2-second
// interval as long as the corresponding unit is "active" or "activating".
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface JabaliCheckResult {
  current_sha: string;
  remote_sha: string;
  behind_count: number;
  branch: string;
}

export interface AptPackage {
  name: string;
  current: string;
  new: string;
  source: string;
}

export interface AptCheckResult {
  packages: AptPackage[];
  total: number;
}

export interface RunResult {
  unit: string;
  started_at: string;
}

export interface UnitStatus {
  unit: string;
  status: string;
  exit_code?: number;
  log_tail: string;
  fetched_at: string;
}

export function useJabaliCheck() {
  return useMutation<JabaliCheckResult>({
    mutationFn: async () => {
      const r = await apiClient.get<JabaliCheckResult>("/admin/updates/jabali/check");
      return r.data;
    },
  });
}

export function useJabaliRun() {
  const qc = useQueryClient();
  return useMutation<RunResult>({
    mutationFn: async () => {
      const r = await apiClient.post<RunResult>("/admin/updates/jabali/run");
      return r.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["jabali-status"] });
    },
  });
}

export function useJabaliStatus(since: string | null) {
  return useQuery<UnitStatus>({
    queryKey: ["jabali-status", since],
    queryFn: async () => {
      const r = await apiClient.get<UnitStatus>(
        `/admin/updates/jabali/status${since ? `?since=${encodeURIComponent(since)}` : ""}`,
      );
      return r.data;
    },
    enabled: !!since,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      // Poll while the unit is alive — 2s gives a snappy log tail. Stop
      // polling once it terminates so we don't hit the agent forever.
      return s === "active" || s === "activating" ? 2000 : false;
    },
  });
}

export function useJabaliStop() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const r = await apiClient.delete("/admin/updates/jabali");
      return r.data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["jabali-status"] }),
  });
}

export function useAptCheck() {
  return useMutation<AptCheckResult>({
    mutationFn: async () => {
      const r = await apiClient.get<AptCheckResult>("/admin/updates/apt/check");
      return r.data;
    },
  });
}

export function useAptRun() {
  const qc = useQueryClient();
  return useMutation<RunResult>({
    mutationFn: async () => {
      const r = await apiClient.post<RunResult>("/admin/updates/apt/run");
      return r.data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["apt-status"] }),
  });
}

export function useAptStatus(since: string | null) {
  return useQuery<UnitStatus>({
    queryKey: ["apt-status", since],
    queryFn: async () => {
      const r = await apiClient.get<UnitStatus>(
        `/admin/updates/apt/status${since ? `?since=${encodeURIComponent(since)}` : ""}`,
      );
      return r.data;
    },
    enabled: !!since,
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "active" || s === "activating" ? 2000 : false;
    },
  });
}

export function useAptStop() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const r = await apiClient.delete("/admin/updates/apt");
      return r.data;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["apt-status"] }),
  });
}
