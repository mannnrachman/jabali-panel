// kratos.ts — thin typed wrapper around the Kratos browser self-service API.
//
// nginx proxies /.ory/* to Kratos's public port (4433), same origin as the
// SPA, so browsers attach the ory_kratos_session cookie automatically.
// We never use the Admin API from the SPA — that's always server-side via
// panel-api.

import axios, { type AxiosError } from "axios";

const KRATOS_BASE = "/.ory";

export const kratosClient = axios.create({
  baseURL: KRATOS_BASE,
  withCredentials: true,
  timeout: 15000,
  headers: { Accept: "application/json" },
});

// ---------------------------------------------------------------------------
// Flow types — minimal shape of what we actually read. Kratos responses have
// many more fields; we intentionally keep the surface small so the renderer
// doesn't overfit to upstream cosmetic changes.
// ---------------------------------------------------------------------------

export type KratosNodeInputAttributes = {
  name: string;
  type: "text" | "password" | "hidden" | "submit" | "email" | "checkbox" | "tel" | "number" | "button";
  value?: string | number | boolean | null;
  required?: boolean;
  disabled?: boolean;
  autocomplete?: string;
  pattern?: string;
};

export type KratosMessage = {
  id: number;
  text: string;
  type: "info" | "error" | "success";
  context?: Record<string, unknown>;
};

export type KratosNode = {
  type: "input" | "img" | "a" | "text" | "script";
  group: string; // "default" | "password" | "totp" | "lookup_secret" | "webauthn" | ...
  attributes: KratosNodeInputAttributes;
  meta?: { label?: { text?: string; id?: number } };
  messages?: KratosMessage[];
};

export type KratosFlow = {
  id: string;
  type: "browser" | "api";
  expires_at: string;
  issued_at: string;
  request_url: string;
  ui: {
    action: string;
    method: "POST" | "GET";
    nodes: KratosNode[];
    messages?: KratosMessage[];
  };
  // Login flows optionally advertise the authenticator-assurance level they
  // require. When we've finished AAL1 (password) and AAL2 is required, Kratos
  // sets `requested_aal: "aal2"` and the UI switches to TOTP / backup-code
  // inputs on the next fetch.
  requested_aal?: "aal1" | "aal2";
  refresh?: boolean;
};

// ---------------------------------------------------------------------------
// API calls
// ---------------------------------------------------------------------------

/**
 * Initialise a login flow. Kratos issues a CSRF token and builds the UI node
 * tree for whichever credential method(s) the identity schema allows. On the
 * browser endpoint, GET redirects to our UI by default — we set
 * `Accept: application/json` to receive the flow object directly instead.
 */
export async function initLoginFlow(): Promise<KratosFlow> {
  const resp = await kratosClient.get<KratosFlow>("/self-service/login/browser");
  return resp.data;
}

/**
 * Re-fetch a flow by id. Used to rehydrate the form after a reload, or to
 * read the AAL2 nodes after Kratos upgrades the flow in response to password
 * success against a 2FA-enabled identity.
 */
export async function getLoginFlow(id: string): Promise<KratosFlow> {
  const resp = await kratosClient.get<KratosFlow>(
    `/self-service/login/flows?id=${encodeURIComponent(id)}`,
  );
  return resp.data;
}

/**
 * Submit a login flow. `body` MUST include csrf_token (copied from the flow's
 * hidden input) plus the credential fields the current AAL expects:
 *   - password: method=password, identifier, password
 *   - totp:     method=totp, totp_code
 *   - lookup_secret: method=lookup_secret, lookup_secret
 *
 * Kratos returns:
 *   200 with an updated flow when more input is needed (e.g. AAL2 step).
 *   200 with {session, session_token?} when login is complete.
 *   302/303 when the browser should follow a return_to redirect.
 */
export async function submitLoginFlow(
  flow: KratosFlow,
  body: Record<string, string | number | boolean>,
): Promise<KratosSubmitResult> {
  try {
    const resp = await kratosClient.post<KratosFlow | KratosSuccess>(flow.ui.action, body);
    // Success response contains a `session` object — if present, we're in.
    const data = resp.data as Partial<KratosSuccess> & Partial<KratosFlow>;
    if (data.session) {
      return { kind: "success", session: data.session };
    }
    // Otherwise Kratos returned an updated flow (likely AAL2 step required).
    if (data.ui) {
      return { kind: "continue", flow: data as KratosFlow };
    }
    return { kind: "error", message: "Unexpected response from identity provider" };
  } catch (err) {
    const ax = err as AxiosError<KratosFlow>;
    // 400 on a login flow with errors embedded in the flow's ui.messages is
    // the normal "wrong password" / "rate limited" path — surface the flow
    // so the caller can re-render with the error messages in place.
    if (ax.response?.status === 400 && ax.response.data?.ui) {
      return { kind: "continue", flow: ax.response.data };
    }
    return { kind: "error", message: humanizeKratosError(ax) };
  }
}

export type KratosSession = {
  id: string;
  active: boolean;
  identity: {
    id: string;
    traits: { email: string; username?: string; is_admin?: boolean };
  };
};

type KratosSuccess = {
  session: KratosSession;
  session_token?: string;
};

export type KratosSubmitResult =
  | { kind: "success"; session: KratosSession }
  | { kind: "continue"; flow: KratosFlow }
  | { kind: "error"; message: string };

/**
 * Who am I? Returns null when there's no active session — we distinguish
 * "not logged in" (401) from transient upstream errors (5xx / network)
 * so authProvider.check() can route to /login cleanly on the former and
 * surface a retry toast on the latter.
 */
export async function whoami(): Promise<KratosSession | null> {
  try {
    const resp = await kratosClient.get<KratosSession>("/sessions/whoami");
    return resp.data;
  } catch (err) {
    const ax = err as AxiosError;
    if (ax.response?.status === 401) return null;
    // For 5xx/network we re-throw so the caller can show a transient toast
    // rather than a silent logout on a Kratos blip.
    throw err;
  }
}

/**
 * Kick off the browser logout. Kratos returns a token + URL; the caller
 * issues a POST to the URL with the token to invalidate the session.
 * We wrap it into a single call that returns once the cookie is cleared.
 */
export async function logoutBrowser(): Promise<void> {
  const resp = await kratosClient.get<{ logout_token: string; logout_url: string }>(
    "/self-service/logout/browser",
  );
  // Kratos expects a GET on logout_url with the token as a query param for
  // browser flows. withCredentials ensures the cookie is sent so the session
  // row can be deleted server-side.
  await kratosClient.get(resp.data.logout_url, {
    params: { token: resp.data.logout_token },
    withCredentials: true,
  });
}

function humanizeKratosError(err: AxiosError): string {
  const status = err.response?.status;
  if (!status) return "Network error — could not reach identity service";
  if (status === 429) return "Too many attempts — try again in a minute";
  if (status >= 500) return "Identity service temporarily unavailable";
  return err.message ?? "Login failed";
}

// ---------------------------------------------------------------------------
// Renderer helpers — project flow.ui.nodes to a flat shape the React form
// can render without caring about Kratos's internal taxonomy.
// ---------------------------------------------------------------------------

export type RenderableField = {
  name: string;
  kind: "text" | "email" | "password" | "tel" | "number" | "hidden" | "submit";
  value: string;
  label?: string;
  required: boolean;
  disabled: boolean;
  autocomplete?: string;
  group: string;
  errors: string[];
};

/**
 * Extract the visible + hidden fields from a flow for a specific group
 * (typically "password" first, then "totp" after the AAL2 step). The
 * "default" group is always included because it carries the CSRF token
 * and the flow's method + action metadata.
 */
export function renderableFields(flow: KratosFlow, group: string): RenderableField[] {
  const out: RenderableField[] = [];
  for (const node of flow.ui.nodes) {
    if (node.type !== "input") continue;
    if (node.group !== "default" && node.group !== group) continue;
    const attrs = node.attributes;
    const type = attrs.type;
    if (type === "checkbox" || type === "button") {
      // Kratos sometimes emits non-input types we don't render directly.
      continue;
    }
    const value = attrs.value === undefined || attrs.value === null ? "" : String(attrs.value);
    out.push({
      name: attrs.name,
      kind: type,
      value,
      label: node.meta?.label?.text,
      required: !!attrs.required,
      disabled: !!attrs.disabled,
      autocomplete: attrs.autocomplete,
      group: node.group,
      errors: (node.messages ?? []).filter((m) => m.type === "error").map((m) => m.text),
    });
  }
  return out;
}

/**
 * Flat list of top-level flow messages (not per-field). Kratos uses these
 * for cross-field errors like "invalid csrf token" or "account locked".
 */
export function flowMessages(flow: KratosFlow): string[] {
  return (flow.ui.messages ?? []).filter((m) => m.type === "error").map((m) => m.text);
}

/**
 * Pull the csrf_token hidden input's value out of the flow. Missing means
 * this is an API flow (we only use browser flows, so this should never
 * be missing in practice — but callers should tolerate empty gracefully).
 */
export function csrfToken(flow: KratosFlow): string {
  for (const node of flow.ui.nodes) {
    if (node.type === "input" && node.attributes.name === "csrf_token") {
      return String(node.attributes.value ?? "");
    }
  }
  return "";
}
