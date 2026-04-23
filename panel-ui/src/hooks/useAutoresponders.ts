// useAutoresponders.ts — M6.5 email autoresponders hooks.
//
// M6.5 Step 1: Stub placeholder.
// Implementation: Wave B (m65/email-autoresponders).
//
// TODO: Implement hooks for:
//   - GET    /mailboxes/:id/autoresponders     → useAutoresponders
//   - POST   /mailboxes/:id/autoresponders     → useCreateAutoresponder
//   - DELETE /autoresponders/:id               → useDeleteAutoresponder
//   - PATCH  /autoresponders/:id               → useUpdateAutoresponder

import { type UseQueryResult, type UseMutationResult } from "@tanstack/react-query"

interface Autoresponder {
  id: string
  mailboxID: string
  subject: string
  message: string
  enabled: boolean
  startDate?: string
  endDate?: string
  createdAt: string
  updatedAt: string
}

export function useAutoresponders(): UseQueryResult<
  { items: Autoresponder[]; total: number },
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

export function useCreateAutoresponder(): UseMutationResult<
  Autoresponder,
  Error,
  {
    mailboxID: string
    subject: string
    message: string
    enabled?: boolean
    startDate?: string
    endDate?: string
  },
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

export function useDeleteAutoresponder(): UseMutationResult<
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

export function useUpdateAutoresponder(): UseMutationResult<
  Autoresponder,
  Error,
  {
    id: string
    subject?: string
    message?: string
    enabled?: boolean
    startDate?: string
    endDate?: string
  },
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
