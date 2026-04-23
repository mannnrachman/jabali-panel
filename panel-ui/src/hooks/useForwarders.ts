// useForwarders.ts — M6.5 email forwarders hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave B (m65/email-forwarders).
//
// TODO: Implement hooks for:
//   - GET    /mailboxes/:id/forwarders?page=…   → useForwarders
//   - POST   /mailboxes/:id/forwarders          → useCreateForwarder
//   - DELETE /forwarders/:id                    → useDeleteForwarder
//   - PATCH  /forwarders/:id                    → useUpdateForwarder

import { type UseQueryResult, type UseMutationResult } from "@tanstack/react-query"

interface Forwarder {
  id: string
  mailboxID: string
  destinationEmail: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export function useForwarders(): UseQueryResult<
  { items: Forwarder[]; total: number },
  Error
> {
  // TODO: Implement after Wave B lands
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

export function useCreateForwarder(): UseMutationResult<
  Forwarder,
  Error,
  { mailboxID: string; destinationEmail: string },
  unknown
> {
  // TODO: Implement after Wave B lands
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

export function useDeleteForwarder(): UseMutationResult<
  void,
  Error,
  string,
  unknown
> {
  // TODO: Implement after Wave B lands
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

export function useUpdateForwarder(): UseMutationResult<
  Forwarder,
  Error,
  { id: string; destinationEmail?: string; enabled?: boolean },
  unknown
> {
  // TODO: Implement after Wave B lands
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
