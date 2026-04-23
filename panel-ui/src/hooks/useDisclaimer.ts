// useDisclaimer.ts — M6.5 domain-level disclaimer hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave C (m65/domain-disclaimer).
//
// TODO: Implement hooks for:
//   - GET    /domains/:id/disclaimer            → useDomainDisclaimer
//   - POST   /domains/:id/disclaimer            → useCreateDomainDisclaimer
//   - DELETE /domains/:id/disclaimer            → useDeleteDomainDisclaimer
//   - PATCH  /domains/:id/disclaimer            → useUpdateDomainDisclaimer

import { type UseQueryResult, type UseMutationResult } from "@tanstack/react-query"

interface DomainDisclaimer {
  id: string
  domainID: string
  text: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export function useDomainDisclaimer(): UseQueryResult<
  DomainDisclaimer | null,
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

export function useCreateDomainDisclaimer(): UseMutationResult<
  DomainDisclaimer,
  Error,
  { domainID: string; text: string; enabled?: boolean },
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

export function useDeleteDomainDisclaimer(): UseMutationResult<
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

export function useUpdateDomainDisclaimer(): UseMutationResult<
  DomainDisclaimer,
  Error,
  { domainID: string; text?: string; enabled?: boolean },
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
