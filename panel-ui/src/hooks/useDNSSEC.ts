// useDNSSEC — TanStack Query wrappers for the DNSSEC endpoints
// mounted in panel-api/internal/api/domain_dnssec.go.
//
// Three endpoints:
//   GET  /domains/:id/dnssec        — current state + cached keys
//   PUT  /domains/:id/dnssec        — flip enabled on/off
//   GET  /domains/:id/dnssec/ds     — DS records for registrar
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export interface DNSSECKey {
  key_tag: number;
  key_type: "KSK" | "ZSK" | "CSK";
  algorithm: number;
  public_key: string;
  active: boolean;
}

export interface DNSSECState {
  domain_id: string;
  domain_name: string;
  enabled: boolean;
  enabled_at?: string;
  keys: DNSSECKey[];
}

export interface DSRecord {
  key_tag: number;
  algorithm: number;
  digest_type: number;
  digest: string;
}

export interface DSResponse {
  domain_id: string;
  domain_name: string;
  ds_records: DSRecord[];
}

const keys = {
  detail: (id: string) => ["dnssec", id] as const,
  ds: (id: string) => ["dnssec", id, "ds"] as const,
};

export function useDNSSECState(domainID: string | undefined) {
  return useQuery<DNSSECState>({
    queryKey: keys.detail(domainID ?? ""),
    queryFn: async () => {
      const res = await apiClient.get<DNSSECState>(`/domains/${domainID}/dnssec`);
      return res.data;
    },
    enabled: !!domainID,
  });
}

export function useUpdateDNSSEC(domainID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (enabled: boolean): Promise<DNSSECState> => {
      const res = await apiClient.put<DNSSECState>(`/domains/${domainID}/dnssec`, {
        enabled,
      });
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dnssec"] });
      qc.invalidateQueries({ queryKey: ["domains"] });
    },
  });
}

export function useDSRecords(domainID: string | undefined, enabled: boolean) {
  return useQuery<DSResponse>({
    queryKey: keys.ds(domainID ?? ""),
    queryFn: async () => {
      const res = await apiClient.get<DSResponse>(`/domains/${domainID}/dnssec/ds`);
      return res.data;
    },
    enabled: !!domainID && enabled,
  });
}

export function algorithmLabel(algo: number): string {
  switch (algo) {
    case 8:
      return "RSASHA256";
    case 10:
      return "RSASHA512";
    case 13:
      return "ECDSAP256SHA256";
    case 14:
      return "ECDSAP384SHA384";
    case 15:
      return "ED25519";
    case 16:
      return "ED448";
    default:
      return `alg-${algo}`;
  }
}

export function digestTypeLabel(dt: number): string {
  switch (dt) {
    case 1:
      return "SHA-1";
    case 2:
      return "SHA-256";
    case 4:
      return "SHA-384";
    default:
      return `type-${dt}`;
  }
}
