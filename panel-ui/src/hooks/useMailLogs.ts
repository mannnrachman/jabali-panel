// useMailLogs.ts — M6.5 email audit logs hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave D (m65/mail-logs).
//
// TODO: Implement hooks for:
//   - GET    /mailboxes/:id/logs?page=…        → useMailboxLogs

import { type UseQueryResult } from "@tanstack/react-query"

interface MailLog {
  id: string
  mailboxID: string
  timestamp: string
  action: string
  details: string
  ipAddress?: string
}

export function useMailboxLogs(): UseQueryResult<
  { items: MailLog[]; total: number },
  Error
> {
  // TODO: Implement after Wave D lands
  return {
    data: undefined,
    error: null,
    isLoading: true,
    isError: false,
    isSuccess: false,
    status: "pending",
    dataUpdatedAt: 0,
    errorUpdatedAt: 0,
    failureCount: 0,
    failureReason: null,
    isFetched: false,
    isFetchedAfterMount: false,
    isFetching: false,
    isInitialLoading: true,
    isPaused: false,
    isPending: true,
    isPlaceholderData: false,
    isRefetching: false,
    isStale: true,
    refetch: async () => ({} as any),
  } as any
}
