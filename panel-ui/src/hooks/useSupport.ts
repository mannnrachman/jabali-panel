// useSupport — TanStack mutation for the diagnostic-report endpoint.
//
// POST /admin/support/diagnostic →
//   agent collects + redacts + uploads to enclosed.jabali-panel.com
//   returns {url, password, note_id, ...}
//
// Operator forwards URL+password to support via mailto: link built
// client-side (DiagnosticReportModal). No notify endpoint needed.
import { useMutation } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface DiagnosticReport {
  url: string;
  password: string;
  note_id: string;
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
