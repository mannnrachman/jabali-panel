// useSupport — TanStack mutations for the diagnostic-report flow.
//
// Two steps:
//   1. POST /admin/support/diagnostic
//      → agent collects + redacts + uploads to enclosed.jabali-panel.com
//      → returns {url, password, ntfy_url, ...}
//   2. POST /admin/support/diagnostic/notify  (operator clicks "Send")
//      → agent posts URL+password to ntfy.jabali-panel.com/<topic>
import { useMutation } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface DiagnosticReport {
  url: string;
  password: string;
  note_id: string;
  ntfy_url: string;
  ntfy_topic: string;
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

export interface DiagnosticNotifyParams {
  url: string;
  password: string;
  note?: string;
}

export function useDiagnosticNotify() {
  return useMutation<{ ok: boolean }, Error, DiagnosticNotifyParams>({
    mutationFn: async (params) => {
      const r = await apiClient.post<{ ok: boolean }>(
        "/admin/support/diagnostic/notify",
        params,
      );
      return r.data;
    },
  });
}
