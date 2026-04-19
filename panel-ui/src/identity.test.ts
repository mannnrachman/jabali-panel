// Identity cache smoke test. Stubs apiClient so we can exercise the
// memoization + clearIdentity paths without a live backend.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "./apiClient";
import { clearIdentity, getIdentity } from "./identity";

describe("identity", () => {
  beforeEach(() => {
    clearIdentity();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("caches the /me response across calls", async () => {
    const get = vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { id: "01K...", email: "a@b.c", is_admin: true },
    } as never);

    const a = await getIdentity();
    const b = await getIdentity();

    expect(a).toEqual({ id: "01K...", email: "a@b.c", isAdmin: true, impersonatedBy: null });
    expect(b).toBe(a);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("coalesces concurrent callers into one request", async () => {
    const get = vi.spyOn(apiClient, "get").mockImplementation(async () => ({
      data: { id: "x", email: "e", is_admin: false },
    } as never));

    const [a, b, c] = await Promise.all([getIdentity(), getIdentity(), getIdentity()]);
    expect(a).toEqual({ id: "x", email: "e", isAdmin: false, impersonatedBy: null });
    expect(a).toBe(b);
    expect(b).toBe(c);
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("returns null when /me fails (e.g. not logged in)", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue(new Error("401"));
    const me = await getIdentity();
    expect(me).toBeNull();
  });

  it("refetches after clearIdentity()", async () => {
    const get = vi
      .spyOn(apiClient, "get")
      .mockResolvedValueOnce({ data: { id: "1", email: "a", is_admin: false } } as never)
      .mockResolvedValueOnce({ data: { id: "2", email: "b", is_admin: true } } as never);

    const first = await getIdentity();
    expect(first?.id).toBe("1");
    clearIdentity();
    const second = await getIdentity();
    expect(second?.id).toBe("2");
    expect(get).toHaveBeenCalledTimes(2);
  });
});
