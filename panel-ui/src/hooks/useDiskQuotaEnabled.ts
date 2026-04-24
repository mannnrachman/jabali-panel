// useDiskQuotaEnabled — global toggle from /admin/settings.disk_quota_enabled.
// Controls whether the Packages form lets the operator edit per-user
// disk-quota limits and whether the reconciler enforces them.
//
// Defaults to `false` while loading: a brief disabled-input flash on the
// edit form is preferable to letting the admin type a quota that won't
// actually be applied because the global flag turned out to be off.
import { useQuery } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

type ServerSettings = {
  disk_quota_enabled?: boolean;
};

const KEY = ["server-settings", "disk-quota-enabled"] as const;

export function useDiskQuotaEnabled() {
  const query = useQuery<ServerSettings>({
    queryKey: KEY,
    queryFn: async () => {
      const { data } = await apiClient.get<ServerSettings>("/admin/settings");
      return data;
    },
    staleTime: 30_000,
    retry: 1,
  });

  return {
    enabled: query.data?.disk_quota_enabled ?? false,
    isLoading: query.isLoading,
  };
}
