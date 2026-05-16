// useRootTerminalEnabled — global gate from
// /admin/settings.root_terminal_enabled (M45, ADR-0096). Drives
// whether the admin Terminal page renders the shell or an
// enable-instructions notice. Defaults false while loading so the
// off state never flashes the live terminal.
import { useQuery } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

type ServerSettings = {
  root_terminal_enabled?: boolean;
};

const KEY = ["server-settings", "root-terminal-enabled"] as const;

export function useRootTerminalEnabled() {
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
    enabled: query.data?.root_terminal_enabled ?? false,
    isLoading: query.isLoading,
  };
}
