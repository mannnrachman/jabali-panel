// useSettingsEmail.test.tsx — Settings → Email hook contract tests.
//
// The wire contract is critical here: panel-api returns two distinct
// response shapes keyed on HTTP status code (200 vs 202). Regressing
// from "discriminate on status" to "discriminate on field presence"
// would still type-check but would break the initializing-state path
// in production (the 202 body has different fields).
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSettingsEmail } from "./useSettingsEmail";

vi.mock("../apiClient", () => ({
  apiClient: {
    get: vi.fn(),
  },
}));

import { apiClient } from "../apiClient";

const wrapper = ({ children }: { children: ReactNode }) => {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
    },
  });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
};

describe("useSettingsEmail", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("maps 200 to { state: 'ready' } with all fields", async () => {
    (apiClient.get as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      status: 200,
      data: {
        primary_domain_name: "jabali-panel.local",
        webmail_url: "https://mail.jabali-panel.local/",
        dkim_published: true,
        email_enabled_at: "2026-04-22T18:00:00Z",
      },
    });

    const { result } = renderHook(() => useSettingsEmail(), { wrapper });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    const d = result.current.data;
    if (!d || d.state !== "ready") {
      throw new Error("expected state ready");
    }
    expect(d.primaryDomainName).toBe("jabali-panel.local");
    expect(d.webmailURL).toBe("https://mail.jabali-panel.local/");
    expect(d.dkimPublished).toBe(true);
    expect(d.emailEnabledAt).toBe("2026-04-22T18:00:00Z");
  });

  it("maps 202 to { state: 'initializing' }", async () => {
    (apiClient.get as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      status: 202,
      data: {
        primary_domain_name: null,
        status: "initializing",
      },
    });

    const { result } = renderHook(() => useSettingsEmail(), { wrapper });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    expect(result.current.data?.state).toBe("initializing");
  });

  it("maps 200 with DKIM not yet published to { dkimPublished: false }", async () => {
    (apiClient.get as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      status: 200,
      data: {
        primary_domain_name: "jabali-panel.local",
        webmail_url: "https://mail.jabali-panel.local/",
        dkim_published: false,
        email_enabled_at: null,
      },
    });

    const { result } = renderHook(() => useSettingsEmail(), { wrapper });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    const d = result.current.data;
    if (!d || d.state !== "ready") {
      throw new Error("expected ready");
    }
    expect(d.dkimPublished).toBe(false);
    expect(d.emailEnabledAt).toBeNull();
  });

  it("surfaces non-2xx errors as an error state", async () => {
    const axiosErr = Object.assign(new Error("boom"), {
      isAxiosError: true,
      response: { data: { error: "internal" } },
    });
    (apiClient.get as ReturnType<typeof vi.fn>).mockRejectedValueOnce(axiosErr);

    const { result } = renderHook(() => useSettingsEmail(), { wrapper });

    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
  });
});
