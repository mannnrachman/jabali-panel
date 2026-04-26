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

export type HubMutateInput = {
  type: string;
  name: string;
  force?: boolean;
};

export function useInstallCrowdsecHubItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: HubMutateInput) => {
      const { data } = await apiClient.post(`${BASE}/hub/install`, input);
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "hub"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "scenarios"] });
    },
  });
}

export function useRemoveCrowdsecHubItem() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { type: string; name: string }) => {
      const { data } = await apiClient.delete(`${BASE}/hub`, {
        params: { type: input.type, name: input.name },
      });
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "hub"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "scenarios"] });
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

// Allowlists (ADR-0061) — server-wide IP/CIDR never-ban list. Single
// allowlist named "jabali-admin-allowlist" lives in LAPI; jabali shells
// to cscli via the agent. LAPI is truth (not jabali DB).
export type CrowdsecAllowlistEntry = {
  value: string;
  reason: string;
  created_at: string;
};

export function useCrowdsecAllowlists() {
  return useQuery({
    queryKey: ["security", "crowdsec", "allowlists"],
    queryFn: async () => {
      const { data } = await apiClient.get<{ items: CrowdsecAllowlistEntry[] }>(
        `${BASE}/allowlists`,
      );
      return data.items ?? [];
    },
  });
}

export type AddCrowdsecAllowlistInput = {
  value: string;
  reason: string;
};

export function useAddCrowdsecAllowlist() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: AddCrowdsecAllowlistInput) => {
      const { data } = await apiClient.post<{ value: string }>(
        `${BASE}/allowlists`,
        input,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "allowlists"] });
    },
  });
}

export function useRemoveCrowdsecAllowlist() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (value: string) => {
      await apiClient.delete(`${BASE}/allowlists`, {
        params: { value },
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "allowlists"] });
    },
  });
}

// Alerts (M27 step 3) — read-only. Alerts = scenario fires; decisions =
// active enforcement. Upstream caps the list at 100 entries/24h server-side.
export type CrowdsecAlert = {
  id: number;
  scenario: string;
  source_ip: string;
  source_scope: string;
  source_value: string;
  events_count: number;
  decisions_count: number;
  started_at: string;
  stopped_at: string;
  machine_id: string;
};

export function useCrowdsecAlerts() {
  return useQuery({
    queryKey: ["security", "crowdsec", "alerts"],
    queryFn: async () => {
      const { data } = await apiClient.get<{ items: CrowdsecAlert[] }>(
        `${BASE}/alerts`,
      );
      return data.items ?? [];
    },
    refetchInterval: 60_000,
  });
}

// Alert detail shape is deliberately loose — cscli inspect returns a rich
// tree (meta + events + decisions + source) that the UI renders via
// nested tables. Keep as `unknown` and let the component narrow.
export function useCrowdsecAlert(id: number | null) {
  return useQuery({
    queryKey: ["security", "crowdsec", "alerts", id],
    enabled: id !== null && id > 0,
    queryFn: async () => {
      const { data } = await apiClient.get<{ alert: unknown }>(
        `${BASE}/alerts/${id}`,
      );
      return data.alert;
    },
  });
}

// Console enrollment (M27 step 4, ADR-0062). Enroll-only; operator
// disenrolls from app.crowdsec.net. No GET — state isn't reliably
// distinguishable from /etc/crowdsec config files.
export type EnrollCrowdsecConsoleInput = {
  key: string;
  name?: string;
};

export function useEnrollCrowdsecConsole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: EnrollCrowdsecConsoleInput) => {
      const { data } = await apiClient.post<{ pending: boolean }>(
        `${BASE}/console/enroll`,
        input,
      );
      return data;
    },
    onSuccess: () => {
      // Enrollment writes online_api_credentials.yaml — refetch state
      // so the card flips from form view to enrolled view.
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "console", "enrollment"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "console", "status"] });
    },
  });
}

// Console share options. cscli has status / enable / disable for
// five preferences controlling which data gets forwarded to Console:
// custom / manual / tainted / context / console_management.
export type CrowdsecConsoleOption = {
  name: string;
  enabled: boolean;
  description: string;
};

export function useCrowdsecConsoleStatus() {
  return useQuery({
    queryKey: ["security", "crowdsec", "console", "status"],
    queryFn: async () => {
      const { data } = await apiClient.get<{ items: CrowdsecConsoleOption[] }>(
        `${BASE}/console/status`,
      );
      return data.items ?? [];
    },
  });
}

export type CrowdsecConsoleEnrollment = {
  enrolled: boolean;
  login?: string;
  url?: string;
  capi_ok: boolean;
};

export function useDisenrollCrowdsecConsole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const { data } = await apiClient.post<{ disenrolled: boolean }>(
        `${BASE}/console/disenroll`,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "console", "enrollment"] });
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "console", "status"] });
    },
  });
}

export function useCrowdsecConsoleEnrollment() {
  return useQuery({
    queryKey: ["security", "crowdsec", "console", "enrollment"],
    queryFn: async () => {
      const { data } = await apiClient.get<CrowdsecConsoleEnrollment>(
        `${BASE}/console/enrollment`,
      );
      return data;
    },
  });
}

export function useToggleCrowdsecConsoleOption() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: { option: string; enabled: boolean }) => {
      const verb = input.enabled ? "enable" : "disable";
      const { data } = await apiClient.post<{ option: string; enabled: boolean }>(
        `${BASE}/console/options/${encodeURIComponent(input.option)}/${verb}`,
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "console", "status"] });
    },
  });
}

// Captcha remediation (M27 step 5). Server-settings DB is truth for the
// toggle + creds; agent rewrites bouncer conf on every Save. Secret is
// write-only (never returned by GET).
export type CrowdsecCaptchaProvider = "" | "hcaptcha" | "recaptcha" | "turnstile";

export type CrowdsecCaptchaConfig = {
  enabled: boolean;
  provider: CrowdsecCaptchaProvider;
  site_key: string;
};

export type UpdateCrowdsecCaptchaInput = CrowdsecCaptchaConfig & {
  secret_key: string; // empty string = don't change
};

export function useCrowdsecCaptcha() {
  return useQuery({
    queryKey: ["security", "crowdsec", "captcha"],
    queryFn: async () => {
      const { data } = await apiClient.get<CrowdsecCaptchaConfig>(`${BASE}/captcha`);
      return data;
    },
  });
}

export function useUpdateCrowdsecCaptcha() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (input: UpdateCrowdsecCaptchaInput) => {
      const { data } = await apiClient.put<CrowdsecCaptchaConfig>(`${BASE}/captcha`, input);
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "captcha"] });
    },
  });
}

// Per-scenario remediation override (M27 step 6, ADR-0063). Rewrites
// the jabali marker-bounded block in /etc/crowdsec/profiles.yaml.
// captcha_enabled from server_settings (Step 5) gates the captcha option.
export type CrowdsecScenarioItem = {
  name: string;
  description: string;
};

export type CrowdsecProfileOverride = {
  scenario: string;
  action: "captcha" | "off";
};

export type CrowdsecProfilesView = {
  scenarios: CrowdsecScenarioItem[];
  overrides: CrowdsecProfileOverride[];
  captcha_enabled: boolean;
};

export function useCrowdsecProfiles() {
  return useQuery({
    queryKey: ["security", "crowdsec", "profiles"],
    queryFn: async () => {
      const { data } = await apiClient.get<CrowdsecProfilesView>(`${BASE}/profiles`);
      return data;
    },
  });
}

export function useUpdateCrowdsecProfiles() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (overrides: CrowdsecProfileOverride[]) => {
      const { data } = await apiClient.put<{ overrides: CrowdsecProfileOverride[] }>(
        `${BASE}/profiles`,
        { overrides },
      );
      return data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["security", "crowdsec", "profiles"] });
    },
  });
}
