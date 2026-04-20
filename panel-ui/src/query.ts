// query.ts — single TanStack Query client for the whole SPA.
//
// Instantiated here and imported by main.tsx (once Wave B wires it up)
// so every hook under src/hooks/useQueries.ts shares the same cache.
// Defaults aim for a hosting-panel-shaped workload: list pages feel
// "live" (30s stale), one-shot errors don't pile up retry storms
// (retry: 1), and the tab-switch refetch that useQuery turns on by
// default is noise here — panel-api already fires websockets where
// it matters.
import { QueryClient } from "@tanstack/react-query";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});
