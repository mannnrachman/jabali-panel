// usePanelCertificate — TanStack Query for /admin/panel-certificate
// (M32, ADR-0066).
//
// The endpoint returns the singleton panel_certificate row PLUS a live
// routability decision (computed on the panel-api side every request)
// so the UI's "Use Let's Encrypt" toggle can render its enabled state
// without a separate request.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface PanelCertificate {
  id: number;
  hostname: string;
  status:
    | "self_signed"
    | "pending_acme"
    | "issued"
    | "pending_acme_retry"
    | "failed";
  cert_pem_path: string;
  issued_at?: string | null;
  expires_at?: string | null;
  last_error?: string | null;
  attempt_count: number;
  next_retry_at?: string | null;
  staging: boolean;
  use_le: boolean;
  updated_at: string;
  // Live fields layered on by the GET handler — not stored.
  routable: boolean;
  routable_reason?: string;
}

const QK = ["admin", "panel-certificate"] as const;

export function usePanelCertificate() {
  return useQuery<PanelCertificate>({
    queryKey: QK,
    queryFn: async () => {
      const r = await apiClient.get<PanelCertificate>(
        "/admin/panel-certificate",
      );
      return r.data;
    },
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
}

export function usePanelCertificateToggle() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (patch: { use_le?: boolean; staging?: boolean }) => {
      const r = await apiClient.post<PanelCertificate>(
        "/admin/panel-certificate/toggle",
        patch,
      );
      return r.data;
    },
    onSuccess: (data) => {
      qc.setQueryData(QK, data);
    },
  });
}

export function usePanelCertificateIssue() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const r = await apiClient.post<PanelCertificate>(
        "/admin/panel-certificate/issue",
      );
      return r.data;
    },
    onSuccess: (data) => {
      qc.setQueryData(QK, data);
    },
  });
}
