// Consent page render + Allow/Deny tests.
//
// Stubs the two network calls the page makes (fetch for metadata +
// fetch for accept/deny) so we exercise the render path and the
// submit paths without a live panel-api / Hydra. Assertions focus on
// the UX invariants the plan calls out:
//
//   - Unknown-scope fallback copy ("Unknown scope: ...") renders
//     visibly so a reviewer notices the gap before approving. The
//     backend returns this label for any scope not in
//     hydraclient.ScopeLabels; the SPA must not hide it.
//   - Subject is shown so the user can confirm they're logged in as
//     the right account before consenting.
//   - Expired / unknown challenge surfaces actionable copy, not a
//     raw HTTP status code.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConsentPage } from "./Consent";

// ConsentPage reads only URL params + does its own fetches; it needs
// nothing from the provider chain. Post-M21 we drop the <Refine>
// wrapper entirely and render under MemoryRouter alone.
function renderConsent(initialPath = "/consent?challenge=chal-abc") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/consent" element={<ConsentPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ConsentPage", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let locationHref: string | null;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    // window.location.href setter is restricted in jsdom; intercept
    // via defineProperty so handleAccept/handleDeny redirects are
    // observable instead of throwing.
    locationHref = null;
    Object.defineProperty(window, "location", {
      writable: true,
      value: {
        ...window.location,
        set href(v: string) {
          locationHref = v;
        },
        get href() {
          return locationHref ?? "";
        },
      },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function mockMetadata(body: object) {
    fetchMock.mockImplementationOnce(async (url: string) => {
      if (!url.includes("/api/v1/oauth2/consent/")) {
        throw new Error(`unexpected first fetch URL: ${url}`);
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
  }

  function mockPost(action: "accept" | "deny", body: object, status = 200) {
    fetchMock.mockImplementationOnce(async (url: string, init: RequestInit) => {
      if (!url.includes(`/oauth2-consent/${action}`)) {
        throw new Error(`unexpected second fetch URL: ${url}`);
      }
      if (init.method !== "POST") {
        throw new Error(`expected POST, got ${init.method}`);
      }
      return new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    });
  }

  it("renders the client name, subject, and scope list", async () => {
    mockMetadata({
      client_name: "WordPress @ example.com",
      subject: "user-ulid-abc",
      requested_scope: [
        { scope: "openid", short: "Your identity", long: "Let this app see a stable identifier..." },
        { scope: "email", short: "Email address", long: "Let this app see the email..." },
      ],
    });

    renderConsent();

    await waitFor(() =>
      expect(screen.getByText(/authorize wordpress @ example.com/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/user-ulid-abc/)).toBeInTheDocument();
    expect(screen.getByText(/your identity/i)).toBeInTheDocument();
    expect(screen.getByText(/email address/i)).toBeInTheDocument();
  });

  it("renders unknown-scope fallback copy visibly — never silently drops", async () => {
    // Simulate the backend returning a scope that wasn't in
    // scope_labels.go — the fallback copy starts "Unknown scope:"
    // so the user can notice it before approving.
    mockMetadata({
      client_name: "Test App",
      subject: "user-1",
      requested_scope: [
        { scope: "openid", short: "Your identity", long: "..." },
        {
          scope: "mystery_scope",
          short: "Unknown scope: mystery_scope",
          long: "This scope is not in the panel's label catalog; contact an administrator before approving.",
        },
      ],
    });

    renderConsent();

    await waitFor(() =>
      expect(screen.getByText(/your identity/i)).toBeInTheDocument(),
    );
    // The unknown scope's Short copy must render with the "Unknown"
    // prefix so the reviewer notices. Dropping it would hide a
    // scope the user is about to approve.
    expect(screen.getByText(/unknown scope: mystery_scope/i)).toBeInTheDocument();
  });

  it("posts to /oauth2-consent/accept with full requested scope on Allow", async () => {
    mockMetadata({
      client_name: "App",
      subject: "user-1",
      requested_scope: [
        { scope: "openid", short: "Your identity", long: "..." },
        { scope: "email", short: "Email", long: "..." },
      ],
    });
    mockPost("accept", { redirect_to: "https://panel/oauth2/auth?continue=1" });

    renderConsent();
    const allow = await screen.findByRole("button", { name: /allow/i });
    fireEvent.click(allow);

    await waitFor(() =>
      expect(locationHref).toBe("https://panel/oauth2/auth?continue=1"),
    );
    // Verify the POST body includes challenge + both scopes.
    const postCall = fetchMock.mock.calls[1];
    expect(postCall[0]).toContain("/oauth2-consent/accept");
    const body = JSON.parse(postCall[1].body as string);
    expect(body.challenge).toBe("chal-abc");
    expect(body.grant_scope).toEqual(["openid", "email"]);
  });

  it("posts to /oauth2-consent/deny on Deny and follows redirect", async () => {
    mockMetadata({
      client_name: "App",
      subject: "user-1",
      requested_scope: [{ scope: "openid", short: "Your identity", long: "..." }],
    });
    mockPost("deny", { redirect_to: "https://app/callback?error=access_denied" });

    renderConsent();
    const deny = await screen.findByRole("button", { name: /deny/i });
    fireEvent.click(deny);

    await waitFor(() =>
      expect(locationHref).toBe("https://app/callback?error=access_denied"),
    );
  });

  it("surfaces an actionable message on a 404 challenge (expired or already used)", async () => {
    fetchMock.mockImplementationOnce(async () =>
      new Response("not found", { status: 404 }),
    );

    renderConsent();

    await waitFor(() =>
      expect(
        screen.getByText(/consent request is no longer valid/i),
      ).toBeInTheDocument(),
    );
    // Allow/Deny buttons must NOT render on the error path — the
    // user has nothing to consent to.
    expect(screen.queryByRole("button", { name: /allow/i })).not.toBeInTheDocument();
  });

  it("shows a missing-challenge error when the URL has no challenge param", async () => {
    // No ?challenge=... in the URL → error card, no fetch at all.
    renderConsent("/consent");

    await waitFor(() =>
      expect(screen.getByText(/needs a consent challenge/i)).toBeInTheDocument(),
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
