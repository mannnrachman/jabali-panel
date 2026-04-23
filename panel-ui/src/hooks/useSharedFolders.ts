// useSharedFolders.ts — M6.5 mailbox sharing / shared folders hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave C (m65/mailbox-shares).
//
// TODO: Implement hooks for:
//   - GET    /mailboxes/:id/shares?page=…      → useMailboxShares
//   - POST   /mailboxes/:id/shares             → useCreateMailboxShare
//   - DELETE /shares/:id                       → useDeleteMailboxShare
//   - PATCH  /shares/:id                       → useUpdateMailboxShare

import { type UseQueryResult, type UseMutationResult } from "@tanstack/react-query"

interface MailboxShare {
  id: string
  ownerMailboxID: string
  sharedWithMailboxID: string
  rights: string // JSON-encoded ACL rights
  createdAt: string
  updatedAt: string
}

export function useMailboxShares(): UseQueryResult<
  { items: MailboxShare[]; total: number },
  Error
> {
  // TODO: Implement after Wave C lands
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

export function useCreateMailboxShare(): UseMutationResult<
  MailboxShare,
  Error,
  { ownerMailboxID: string; sharedWithMailboxID: string; rights?: string },
  unknown
> {
  // TODO: Implement after Wave C lands
  return {
    mutate: () => {},
    mutateAsync: async () => ({} as any),
    isPending: false,
    isError: false,
    isSuccess: false,
    isIdle: true,
    data: undefined,
    error: null,
    status: "idle",
    failureCount: 0,
    failureReason: null,
    reset: () => {},
    context: undefined,
    variables: undefined,
  } as any
}

export function useDeleteMailboxShare(): UseMutationResult<
  void,
  Error,
  string,
  unknown
> {
  // TODO: Implement after Wave C lands
  return {
    mutate: () => {},
    mutateAsync: async () => {},
    isPending: false,
    isError: false,
    isSuccess: false,
    isIdle: true,
    data: undefined,
    error: null,
    status: "idle",
    failureCount: 0,
    failureReason: null,
    reset: () => {},
    context: undefined,
    variables: undefined,
  } as any
}

export function useUpdateMailboxShare(): UseMutationResult<
  MailboxShare,
  Error,
  { id: string; rights?: string },
  unknown
> {
  // TODO: Implement after Wave C lands
  return {
    mutate: () => {},
    mutateAsync: async () => ({} as any),
    isPending: false,
    isError: false,
    isSuccess: false,
    isIdle: true,
    data: undefined,
    error: null,
    status: "idle",
    failureCount: 0,
    failureReason: null,
    reset: () => {},
    context: undefined,
    variables: undefined,
  } as any
}
