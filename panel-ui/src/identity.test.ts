// Identity cache smoke test. Stubs the /api/v1/me call so we can exercise
// the memoization + clearIdentity paths without a live backend. Sourcing
// from /me (not Kratos whoami) gives us the panel ULID — see identity.ts
// for the rationale.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "./apiClient";
import { clearIdentity, getIdentity } from "./identity";

type MeResponse = { id: string; email: string; is_admin: boolean };

function meResponse(id: string, email: string, isAdmin: boolean): MeResponse {
  return { id, email, is_admin: isAdmin };
}

describe("identity", () => {
  beforeEach(() => {
    clearIdentity();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("caches the /me response across calls", async () => {
    const spy = vi
      .spyOn(apiClient, "get")
      .mockResolvedValue({ data: meResponse("01K...", "a@b.c", true) });

    const a = await getIdentity();
    const b = await getIdentity();

    expect(a).toEqual({
      id: "01K...",
      email: "a@b.c",
      isAdmin: true,
    });
    expect(b).toBe(a);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("coalesces concurrent callers into one /me call", async () => {
    const spy = vi
      .spyOn(apiClient, "get")
      .mockImplementation(async () => ({ data: meResponse("x", "e", false) }));

    const [a, b, c] = await Promise.all([
      getIdentity(),
      getIdentity(),
      getIdentity(),
    ]);
    expect(a).toEqual({
      id: "x",
      email: "e",
      isAdmin: false,
    });
    expect(a).toBe(b);
    expect(b).toBe(c);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("returns null when /me rejects (401 no session)", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue({
      response: { status: 401 },
    });
    const me = await getIdentity();
    expect(me).toBeNull();
  });

  it("returns null (not throws) when /me fails transiently — blip shouldn't force logout", async () => {
    vi.spyOn(apiClient, "get").mockRejectedValue(new Error("5xx"));
    const me = await getIdentity();
    expect(me).toBeNull();
  });

  it("defaults is_admin to false when the field is missing or non-boolean", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { id: "x", email: "e@x" } as MeResponse,
    });
    const me = await getIdentity();
    expect(me?.isAdmin).toBe(false);
  });

  it("refetches after clearIdentity()", async () => {
    const spy = vi
      .spyOn(apiClient, "get")
      .mockResolvedValueOnce({ data: meResponse("1", "a", false) })
      .mockResolvedValueOnce({ data: meResponse("2", "b", true) });

    const first = await getIdentity();
    expect(first?.id).toBe("1");
    clearIdentity();
    const second = await getIdentity();
    expect(second?.id).toBe("2");
    expect(spy).toHaveBeenCalledTimes(2);
  });
});
