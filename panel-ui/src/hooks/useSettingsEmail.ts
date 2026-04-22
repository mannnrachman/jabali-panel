// useSettingsEmail — M6.4 Settings → Email card hook.
//
// Wire contract (verified against panel-api/internal/api/settings_email.go
// per feedback_verify_wire_contract.md). The endpoint returns two DIFFERENT
// shapes discriminated by HTTP status code, NOT by field presence:
//
//   200 OK   — panel-primary domain row exists, DKIM may or may not be
//              published depending on whether reconciler has converged:
//                { primary_domain_name, webmail_url, dkim_published,
//                  email_enabled_at }
//   202 Accepted — row absent (install still converging, or pathological
//                  operator SQL delete): { primary_domain_name: null,
//                  status: "initializing" }
//
// The hook returns a discriminated union so the card component can pattern-
// match on `.state`. Converging installs get a short refetch interval so
// the UI flips to "Published" within a tick of the reconciler creating
// DKIM — no operator-initiated refresh required.

import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import axios, { type AxiosError } from "axios";

import { apiClient } from "../apiClient";

export interface SettingsEmailReady {
  state: "ready";
  primaryDomainName: string;
  webmailURL: string;
  dkimPublished: boolean;
  emailEnabledAt: string | null;
}

export interface SettingsEmailInitializing {
  state: "initializing";
}

export type SettingsEmail = SettingsEmailReady | SettingsEmailInitializing;

interface OkWire {
  primary_domain_name: string;
  webmail_url: string;
  dkim_published: boolean;
  email_enabled_at: string | null;
}

interface InitWire {
  primary_domain_name: null;
  status: "initializing";
}

// Polls every 10s while initializing so the UI flips from "Initializing"
// to "Published" within one tick of the reconciler converging DKIM. Stops
// polling once state == "ready".
const INITIALIZING_REFETCH_MS = 10_000;

export function useSettingsEmail(): UseQueryResult<SettingsEmail, Error> {
  return useQuery<SettingsEmail, Error>({
    queryKey: ["settings", "email"],
    queryFn: async () => {
      try {
        const res = await apiClient.get<OkWire>("/admin/settings/email", {
          // axios throws on non-2xx by default; we treat 202 as success
          // via validateStatus so we can inspect the body without an
          // AxiosError detour.
          validateStatus: (s) => s === 200 || s === 202,
        });
        if (res.status === 202) {
          return { state: "initializing" };
        }
        const d = res.data;
        return {
          state: "ready",
          primaryDomainName: d.primary_domain_name,
          webmailURL: d.webmail_url,
          dkimPublished: d.dkim_published,
          emailEnabledAt: d.email_enabled_at,
        };
      } catch (err) {
        // Non-2xx-other-than-202 → surface to callers as error; axios
        // errors have a `.response` shape but we don't care about the
        // body here.
        if (axios.isAxiosError(err)) {
          const ax = err as AxiosError<{ error?: string }>;
          throw new Error(ax.response?.data?.error ?? ax.message);
        }
        throw err as Error;
      }
    },
    // Refetch every 10s if we're still initializing. React-query lets
    // refetchInterval be a function of the last result.
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      return data.state === "initializing" ? INITIALIZING_REFETCH_MS : false;
    },
    staleTime: 30_000,
  });
}

// Widening helper used to derive bare union checks in tests without
// importing the state string literal.
export const INITIALIZING_STATE = "initializing" as const;
export const READY_STATE = "ready" as const;

// Wire shape union exported only for test fixtures to build realistic
// mocks; production code must not consume this directly.
export type SettingsEmailWire = OkWire | InitWire;
