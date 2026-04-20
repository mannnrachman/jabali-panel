// useQueries.test.tsx — smoke tests that the TanStack hooks fire the
// right HTTP verbs and URLs, and that mutations invalidate the list
// cache so a subsequent list fetches fresh data.
//
// We only mock `apiClient` — the rest (QueryClient, hooks) runs for
// real. That's intentional: the value of this file is catching a
// regression in the cache-key or URL shape, not re-testing TanStack.
import {
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  useCreateMutation,
  useDeleteMutation,
  useListQuery,
  useOneQuery,
  useUpdateMutation,
} from "./useQueries";

vi.mock("../apiClient", () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import { apiClient } from "../apiClient";

const mocked = apiClient as unknown as {
  get: ReturnType<typeof vi.fn>;
  post: ReturnType<typeof vi.fn>;
  patch: ReturnType<typeof vi.fn>;
  delete: ReturnType<typeof vi.fn>;
};

function makeWrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, wrapper };
}

beforeEach(() => {
  mocked.get.mockReset();
  mocked.post.mockReset();
  mocked.patch.mockReset();
  mocked.delete.mockReset();
});

describe("useListQuery", () => {
  it("calls GET /{resource} with page_size on the wire and projects data→items", async () => {
    // panel-api returns `{data, total, page, page_size}` — the hook
    // unwraps `data` to `items` so consumers never see the wire
    // envelope and callers still write `pageSize` in camelCase.
    mocked.get.mockResolvedValueOnce({
      data: { data: [{ id: "u1" }], total: 42, page: 2, page_size: 10 },
    });
    const { wrapper } = makeWrapper();
    const { result } = renderHook(
      () =>
        useListQuery<{ id: string }>({
          resource: "users",
          params: { page: 2, pageSize: 10, q: "alice" },
        }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mocked.get).toHaveBeenCalledWith(
      "/users?page=2&page_size=10&q=alice",
    );
    expect(result.current.items).toEqual([{ id: "u1" }]);
    expect(result.current.total).toBe(42);
  });

  it("falls back to `items` when a backend already returns the projected shape", async () => {
    // Forward-compat: if a future endpoint emits {items, total}
    // directly, the hook still reads it without a code change.
    mocked.get.mockResolvedValueOnce({
      data: { items: [{ id: "x" }], total: 1 },
    });
    const { wrapper } = makeWrapper();
    const { result } = renderHook(
      () => useListQuery<{ id: string }>({ resource: "users" }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.items).toEqual([{ id: "x" }]);
  });

  it("skips empty params", async () => {
    mocked.get.mockResolvedValueOnce({ data: { data: [], total: 0 } });
    const { wrapper } = makeWrapper();
    renderHook(
      () =>
        useListQuery({ resource: "users", params: { q: "", page: 1 } }),
      { wrapper },
    );
    await waitFor(() => expect(mocked.get).toHaveBeenCalled());
    expect(mocked.get).toHaveBeenCalledWith("/users?page=1");
  });
});

describe("useOneQuery", () => {
  it("calls GET /{resource}/{id}", async () => {
    mocked.get.mockResolvedValueOnce({ data: { id: "u1", email: "a@b" } });
    const { wrapper } = makeWrapper();
    const { result } = renderHook(
      () => useOneQuery<{ id: string }>({ resource: "users", id: "u1" }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mocked.get).toHaveBeenCalledWith("/users/u1");
  });

  it("is disabled when id is undefined", async () => {
    const { wrapper } = makeWrapper();
    renderHook(
      () => useOneQuery({ resource: "users", id: undefined }),
      { wrapper },
    );
    // No request should fire.
    await new Promise((r) => setTimeout(r, 10));
    expect(mocked.get).not.toHaveBeenCalled();
  });
});

describe("mutations invalidate list cache", () => {
  it("create → list refetches", async () => {
    mocked.get.mockResolvedValue({ data: { data: [], total: 0 } });
    mocked.post.mockResolvedValueOnce({ data: { id: "new" } });

    const { wrapper } = makeWrapper();
    const list = renderHook(
      () => useListQuery({ resource: "users" }),
      { wrapper },
    );
    await waitFor(() => expect(list.result.current.isSuccess).toBe(true));
    const before = mocked.get.mock.calls.length;

    const mut = renderHook(
      () =>
        useCreateMutation<{ id: string }, { email: string }>({
          resource: "users",
        }),
      { wrapper },
    );
    await act(async () => {
      await mut.result.current.mutateAsync({ email: "x@y" });
    });

    await waitFor(() =>
      expect(mocked.get.mock.calls.length).toBeGreaterThan(before),
    );
    expect(mocked.post).toHaveBeenCalledWith("/users", { email: "x@y" });
  });

  it("update → PATCH /{resource}/{id}", async () => {
    mocked.patch.mockResolvedValueOnce({ data: { id: "u1" } });
    const { wrapper } = makeWrapper();
    const mut = renderHook(
      () =>
        useUpdateMutation<{ id: string }, { email: string }>({
          resource: "users",
        }),
      { wrapper },
    );
    await act(async () => {
      await mut.result.current.mutateAsync({
        id: "u1",
        input: { email: "x@y" },
      });
    });
    expect(mocked.patch).toHaveBeenCalledWith("/users/u1", { email: "x@y" });
  });

  it("delete → DELETE /{resource}/{id}", async () => {
    mocked.delete.mockResolvedValueOnce({});
    const { wrapper } = makeWrapper();
    const mut = renderHook(
      () => useDeleteMutation({ resource: "users" }),
      { wrapper },
    );
    await act(async () => {
      await mut.result.current.mutateAsync({ id: "u1" });
    });
    expect(mocked.delete).toHaveBeenCalledWith("/users/u1");
  });
});
