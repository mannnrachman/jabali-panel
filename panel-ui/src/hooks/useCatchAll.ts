// useCatchAll.ts — M6.5 domain-level catch-all hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave C (m65/domain-catchall).
//
// TODO: Implement hooks for:
//   - GET    /domains/:id/catch-all             → useDomainCatchAll
//   - POST   /domains/:id/catch-all             → useCreateDomainCatchAll
//   - DELETE /domains/:id/catch-all             → useDeleteDomainCatchAll
//   - PATCH  /domains/:id/catch-all             → useUpdateDomainCatchAll

import { type UseQueryResult, type UseMutationResult } from "@tanstack/react-query"

interface DomainCatchAll {
  id: string
  domainID: string
  targetMailboxID: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export function useDomainCatchAll(): UseQueryResult<
  DomainCatchAll | null,
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

export function useCreateDomainCatchAll(): UseMutationResult<
  DomainCatchAll,
  Error,
  { domainID: string; targetMailboxID: string; enabled?: boolean },
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

export function useDeleteDomainCatchAll(): UseMutationResult<
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

export function useUpdateDomainCatchAll(): UseMutationResult<
  DomainCatchAll,
  Error,
  { domainID: string; targetMailboxID?: string; enabled?: boolean },
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
