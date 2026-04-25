// useSupport — TanStack Query mutation for the diagnostic-report endpoint.
import { useMutation } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface DiagnosticReport {
  ciphertext_b64: string;
  byte_count: number;
  generated_at: string;
  redaction_count: number;
  file_count: number;
}

export function useDiagnosticReport() {
  return useMutation<DiagnosticReport>({
    mutationFn: async () => {
      const r = await apiClient.post<DiagnosticReport>("/admin/support/diagnostic");
      return r.data;
    },
  });
}
