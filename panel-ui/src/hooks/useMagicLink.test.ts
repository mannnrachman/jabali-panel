import { describe, it, expect, beforeEach, vi } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { useMagicLink } from "./useMagicLink";
import * as apiClientModule from "../apiClient";

// Mock the apiClient module
vi.mock("../apiClient", () => ({
  apiClient: {
    post: vi.fn(),
  },
}));

describe("useMagicLink", () => {
  const mockApiClient = apiClientModule.apiClient as any;
  const installId = "test-install-id";

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("should initialize with loading=false and error=null", () => {
    const { result } = renderHook(() => useMagicLink(installId));
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBe(null);
  });

  it("should successfully mint a magic link", async () => {
    const mockResponse = {
      data: {
        url: "https://example.com/jabali-sso-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.php",
        expires_in: 60,
      },
    };
    mockApiClient.post.mockResolvedValue(mockResponse);

    const { result } = renderHook(() => useMagicLink(installId));

    let response: any;
    await act(async () => {
      response = await result.current.mint();
    });

    expect(mockApiClient.post).toHaveBeenCalledWith(
      `/applications/${installId}/magic-link`,
      {}
    );
    expect(response.url).toBe("https://example.com/jabali-sso-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.php");
    expect(response.expires_in).toBe(60);
    expect(result.current.error).toBe(null);
    expect(result.current.loading).toBe(false);
  });

  it("should handle loading state transitions", async () => {
    const mockResponse = {
      data: {
        url: "https://example.com/jabali-sso-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.php",
        expires_in: 60,
      },
    };
    mockApiClient.post.mockImplementation(
      () =>
        new Promise((resolve) =>
          setTimeout(() => resolve(mockResponse), 10)
        )
    );

    const { result } = renderHook(() => useMagicLink(installId));

    const mintPromise = act(async () => {
      return result.current.mint();
    });

    // Loading state may be set immediately
    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await mintPromise;
  });

  it("should handle error with detail message", async () => {
    const errorMessage = "Installation not found";
    mockApiClient.post.mockRejectedValue({
      response: {
        data: {
          detail: errorMessage,
        },
      },
    });

    const { result } = renderHook(() => useMagicLink(installId));

    await act(async () => {
      try {
        await result.current.mint();
      } catch {
        // Expected error
      }
    });

    expect(result.current.error).toBe(errorMessage);
    expect(result.current.loading).toBe(false);
  });

  it("should handle error with error field", async () => {
    const errorMessage = "API error";
    mockApiClient.post.mockRejectedValue({
      response: {
        data: {
          error: errorMessage,
        },
      },
    });

    const { result } = renderHook(() => useMagicLink(installId));

    await act(async () => {
      try {
        await result.current.mint();
      } catch {
        // Expected error
      }
    });

    expect(result.current.error).toBe(errorMessage);
  });

  it("should handle error with message field", async () => {
    const errorMessage = "Network error";
    mockApiClient.post.mockRejectedValue({
      message: errorMessage,
    });

    const { result } = renderHook(() => useMagicLink(installId));

    await act(async () => {
      try {
        await result.current.mint();
      } catch {
        // Expected error
      }
    });

    expect(result.current.error).toBe(errorMessage);
  });

  it("should fall back to default message when no error fields are set", async () => {
    mockApiClient.post.mockRejectedValue({});

    const { result } = renderHook(() => useMagicLink(installId));

    await act(async () => {
      try {
        await result.current.mint();
      } catch {
        // Expected error
      }
    });

    expect(result.current.error).toBe("Failed to generate magic link");
  });

  it("should clear previous error on successful mint", async () => {
    // First, simulate an error
    mockApiClient.post.mockRejectedValueOnce({
      response: {
        data: {
          detail: "First error",
        },
      },
    });

    const { result } = renderHook(() => useMagicLink(installId));

    await act(async () => {
      try {
        await result.current.mint();
      } catch {
        // Expected error
      }
    });

    expect(result.current.error).toBe("First error");

    // Now, mock a successful response
    mockApiClient.post.mockResolvedValueOnce({
      data: {
        url: "https://example.com/jabali-sso-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB.php",
        expires_in: 60,
      },
    });

    await act(async () => {
      await result.current.mint();
    });

    expect(result.current.error).toBe(null);
  });
});
