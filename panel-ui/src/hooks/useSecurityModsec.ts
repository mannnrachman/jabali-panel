// useSecurityModsec — TanStack Query hooks for the M26 ModSecurity
// admin endpoints. Wire contract per panel-api/internal/api/security_
// modsec.go (verified against handler).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type ModsecEngineMode = "On" | "Off" | "DetectionOnly";

export type ModsecGlobal = {
  engine_mode: ModsecEngineMode;
  paranoia: number;
};

export type ModsecDomainRow = {
  id: string;
  name: string;
  modsec_enabled: boolean;
};

export type ModsecAuditEntry = {
  ts?: string;
  client?: string;
  uri?: string;
  rule_ids?: string[];
  severity?: string;
  raw?: string;
  parse_error?: boolean;
};

const BASE = "/admin/security/modsec";

export function useModsecGlobal() {
  return useQuery({
    queryKey: ["security", "modsec", "global"],
    queryFn: async () => {
      const { data } = await apiClient.get<ModsecGlobal>(`${BASE}/status`);
      return data;
    },
  });
}

export function useUpdateModsecGlobal() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { engine_mode: ModsecEngineMode; paranoia: number }) => {
      const { data } = await apiClient.put<{ applied: boolean; nginx_reloaded: boolean }>(
        `${BASE}/global`,
        input,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "modsec", "global"] });
    },
  });
}

export type ModsecDomainsListResponse = {
  data: ModsecDomainRow[];
  total: number;
  page: number;
  page_size: number;
};

export function useModsecDomains(page: number, pageSize: number) {
  return useQuery({
    queryKey: ["security", "modsec", "domains", page, pageSize],
    queryFn: async () => {
      const { data } = await apiClient.get<ModsecDomainsListResponse>(
        `${BASE}/domains`,
        { params: { page, page_size: pageSize } },
      );
      return data;
    },
  });
}

export function useUpdateModsecDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, modsec_enabled }: { id: string; modsec_enabled: boolean }) => {
      const { data } = await apiClient.patch<{ id: string; modsec_enabled: boolean }>(
        `${BASE}/domains/${id}`,
        { modsec_enabled },
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "modsec", "domains"] });
    },
  });
}

export function useModsecAudit(lines = 50) {
  return useQuery({
    queryKey: ["security", "modsec", "audit", lines],
    queryFn: async () => {
      const { data } = await apiClient.get<{ entries: ModsecAuditEntry[] }>(
        `${BASE}/audit`,
        { params: { lines } },
      );
      return data.entries ?? [];
    },
    refetchInterval: 30_000,
  });
}
