// useSecurityCrowdsec — TanStack Query hooks for the M26 CrowdSec
// admin endpoints. Wire contract per panel-api/internal/api/security_
// crowdsec.go (verified against handler — see memory
// `feedback_verify_wire_contract`).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../apiClient";

export type CrowdsecStatus = {
  running: boolean;
  lapi_reachable: boolean;
  version?: string;
};

export type CrowdsecDecision = {
  id: number;
  ip: string;
  duration: string;
  reason: string;
  scenario: string;
  until: string;
};

export type CrowdsecBouncer = {
  name: string;
  type: string;
  revoked: boolean;
  last_pull: string;
};

export type CrowdsecMetrics = {
  parsed: number;
  unparsed: number;
  buckets: number;
  decisions_active: number;
  alerts_total: number;
};

export type CrowdsecHubItem = {
  name: string;
  type: string;
  installed: boolean;
  enabled: boolean;
};

export type CrowdsecScope = "ip" | "range" | "country" | "as";

const BASE = "/admin/security/crowdsec";

export function useCrowdsecStatus() {
  return useQuery({
    queryKey: ["security", "crowdsec", "status"],
    queryFn: async () => {
      const { data } = await apiClient.get<CrowdsecStatus>(`${BASE}/status`);
      return data;
    },
    refetchInterval: 30_000,
  });
}

export function useCrowdsecMetrics() {
  return useQuery({
    queryKey: ["security", "crowdsec", "metrics"],
    queryFn: async () => {
      const { data } = await apiClient.get<CrowdsecMetrics>(`${BASE}/metrics`);
      return data;
    },
    refetchInterval: 30_000,
  });
}

export function useCrowdsecDecisions(scope?: CrowdsecScope) {
  return useQuery({
    queryKey: ["security", "crowdsec", "decisions", scope ?? "all"],
    queryFn: async () => {
      const params = scope ? { scope } : undefined;
      const { data } = await apiClient.get<{ decisions: CrowdsecDecision[] }>(
        `${BASE}/decisions`,
        { params },
      );
      return data.decisions ?? [];
    },
  });
}

export function useCrowdsecBouncers() {
  return useQuery({
    queryKey: ["security", "crowdsec", "bouncers"],
    queryFn: async () => {
      const { data } = await apiClient.get<{ bouncers: CrowdsecBouncer[] }>(
        `${BASE}/bouncers`,
      );
      return data.bouncers ?? [];
    },
  });
}

export function useCrowdsecHub() {
  return useQuery({
    queryKey: ["security", "crowdsec", "hub"],
    queryFn: async () => {
      const { data } = await apiClient.get<{ items: CrowdsecHubItem[] }>(
        `${BASE}/hub`,
      );
      return data.items ?? [];
    },
  });
}

export type AddCrowdsecDecisionInput = {
  scope: CrowdsecScope;
  value: string;
  duration: string;
  reason: string;
};

export function useAddCrowdsecDecision() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: AddCrowdsecDecisionInput) => {
      const { data } = await apiClient.post<{ id: number }>(
        `${BASE}/decisions`,
        input,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "decisions"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "metrics"] });
    },
  });
}

export function useDeleteCrowdsecDecision() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) => {
      await apiClient.delete(`${BASE}/decisions/${id}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "decisions"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "metrics"] });
    },
  });
}

// AppSec geoblock — server-wide country allow/deny list applied by
// CrowdSec's AppSec engine (L7, pre-evaluation hook). Server-side
// authoritative state lives in server_settings; the agent mirrors it
// into /etc/crowdsec/appsec-rules/jabali-geoblock.yaml on every set.
export type AppSecGeoblockMode = "off" | "allow" | "deny";

export type AppSecGeoblock = {
  mode: AppSecGeoblockMode;
  countries: string[];
};

export function useAppSecGeoblock() {
  return useQuery({
    queryKey: ["security", "crowdsec", "appsec", "geoblock"],
    queryFn: async () => {
      const { data } = await apiClient.get<AppSecGeoblock>(`${BASE}/appsec/geoblock`);
      return data;
    },
  });
}

export function useUpdateAppSecGeoblock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: AppSecGeoblock) => {
      const { data } = await apiClient.put<AppSecGeoblock>(
        `${BASE}/appsec/geoblock`,
        input,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "appsec", "geoblock"] });
    },
  });
}
