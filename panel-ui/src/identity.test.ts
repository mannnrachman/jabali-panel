// Identity cache smoke test. Stubs the kratos whoami call so we can exercise
// the memoization + clearIdentity paths without a live Kratos instance.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearIdentity, getIdentity } from "./identity";
import * as kratos from "./kratos";

function session(
  id: string,
  email: string,
  isAdmin: boolean,
): kratos.KratosSession {
  return {
    id: "session-" + id,
    active: true,
    identity: {
      id,
      traits: { email, is_admin: isAdmin },
    },
  };
}

describe("identity", () => {
  beforeEach(() => {
    clearIdentity();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("caches the whoami response across calls", async () => {
    const spy = vi
      .spyOn(kratos, "whoami")
      .mockResolvedValue(session("01K...", "a@b.c", true));

    const a = await getIdentity();
    const b = await getIdentity();

    expect(a).toEqual({
      id: "01K...",
      email: "a@b.c",
      isAdmin: true,
      impersonatedBy: null,
    });
    expect(b).toBe(a);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("coalesces concurrent callers into one whoami call", async () => {
    const spy = vi
      .spyOn(kratos, "whoami")
      .mockImplementation(async () => session("x", "e", false));

    const [a, b, c] = await Promise.all([
      getIdentity(),
      getIdentity(),
      getIdentity(),
    ]);
    expect(a).toEqual({
      id: "x",
      email: "e",
      isAdmin: false,
      impersonatedBy: null,
    });
    expect(a).toBe(b);
    expect(b).toBe(c);
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("returns null when whoami resolves to no session (not logged in)", async () => {
    vi.spyOn(kratos, "whoami").mockResolvedValue(null);
    const me = await getIdentity();
    expect(me).toBeNull();
  });

  it("returns null (not throws) when whoami fails transiently — Kratos blip shouldn't force logout", async () => {
    vi.spyOn(kratos, "whoami").mockRejectedValue(new Error("5xx"));
    const me = await getIdentity();
    expect(me).toBeNull();
  });

  it("defaults is_admin to false when the trait is missing or non-boolean", async () => {
    vi.spyOn(kratos, "whoami").mockResolvedValue({
      id: "s",
      active: true,
      identity: { id: "x", traits: { email: "e@x" } },
    });
    const me = await getIdentity();
    expect(me?.isAdmin).toBe(false);
  });

  it("refetches after clearIdentity()", async () => {
    const spy = vi
      .spyOn(kratos, "whoami")
      .mockResolvedValueOnce(session("1", "a", false))
      .mockResolvedValueOnce(session("2", "b", true));

    const first = await getIdentity();
    expect(first?.id).toBe("1");
    clearIdentity();
    const second = await getIdentity();
    expect(second?.id).toBe("2");
    expect(spy).toHaveBeenCalledTimes(2);
  });
});
