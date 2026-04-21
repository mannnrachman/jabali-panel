import { useState } from "react";
import { apiClient } from "../apiClient";

interface MagicLinkResponse {
  url: string;
  expires_in: number;
}

interface UseMagicLinkReturn {
  mint: () => Promise<MagicLinkResponse>;
  loading: boolean;
  error: string | null;
}

export function useMagicLink(installId: string): UseMagicLinkReturn {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mint = async (): Promise<MagicLinkResponse> => {
    setLoading(true);
    setError(null);
    try {
      const response = await apiClient.post(
        `/applications/${installId}/magic-link`,
        {}
      );
      return response.data as MagicLinkResponse;
    } catch (err) {
      const msg =
        (err as {
          response?: { data?: { error?: string; detail?: string } };
          message?: string;
        })?.response?.data?.detail ??
        (err as { response?: { data?: { error?: string } } })?.response?.data
          ?.error ??
        (err as { message?: string })?.message ??
        "Failed to generate magic link";
      setError(msg);
      throw err;
    } finally {
      setLoading(false);
    }
  };

  return { mint, loading, error };
}
