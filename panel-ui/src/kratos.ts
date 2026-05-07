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

/**
 * Re-fetch a settings flow by id. Settings is the post-login flow that
 * lets a user change their password and manage TOTP — Kratos owns the
 * flow object, we just render the nodes inline on the profile page.
 *
 * Same shape as login's getLoginFlow; separate endpoint because Kratos
 * scopes flow ids by flow type.
 */
export async function getSettingsFlow(id: string): Promise<KratosFlow> {
  const resp = await kratosClient.get<KratosFlow>(
    `/self-service/settings/flows?id=${encodeURIComponent(id)}`,
  );
  return resp.data;
}

/**
 * Result of an initSettingsFlow call. Kratos returns the flow on
 * success, asks for a privileged-session refresh on stale sessions,
 * or 401 when there's no session at all.
 */
export type SettingsInitResult =
  | { kind: "flow"; flow: KratosFlow }
  | { kind: "refresh_required" }
  | { kind: "unauthenticated" }
  | { kind: "error"; message: string };

/**
 * Initialise a settings flow without the redirect dance. Kratos's
 * /self-service/settings/browser endpoint normally 303s to the
 * configured ui_url; with `Accept: application/json` it returns the
 * flow JSON directly. The browser flow init is the only Kratos path
 * that performs the privileged-session check, so we still need it
 * for that side-effect — we just sidestep the page reload.
 *
 * Privileged-session expired → 403 with id "session_refresh_required".
 * No session at all → 401.
 */
export async function initSettingsFlow(): Promise<SettingsInitResult> {
  try {
    const resp = await kratosClient.get<KratosFlow>(
      "/self-service/settings/browser",
      // Don't follow the 303 to ui_url — we want the JSON body.
      { headers: { Accept: "application/json" }, maxRedirects: 0 },
    );
    return { kind: "flow", flow: resp.data };
  } catch (err) {
    const ax = err as AxiosError<{ error?: { id?: string } }>;
    const status = ax.response?.status;
    const errorId = ax.response?.data?.error?.id;
    if (status === 403 && errorId === "session_refresh_required") {
      return { kind: "refresh_required" };
    }
    if (status === 401) {
      return { kind: "unauthenticated" };
    }
    return { kind: "error", message: humanizeKratosError(ax) };
  }
}

/**
 * Submit a settings flow update (e.g. password change, TOTP enrolment).
 * Kratos returns:
 *   200 with the updated flow — UI re-renders with success/error in
 *     ui.messages and per-node errors. The flow stays alive so the user
 *     can fix mistakes without re-initialising.
 *   401 when the privileged session has expired — Kratos redirects to
 *     login, we surface that as an error so the caller can prompt
 *     re-authentication.
 *   403 / 422 with a flow body on validation errors — same shape as 200.
 */
export async function submitSettingsFlow(
  flow: KratosFlow,
  body: Record<string, string | number | boolean>,
): Promise<KratosSubmitResult> {
  try {
    const resp = await kratosClient.post<KratosFlow>(flow.ui.action, body);
    if (resp.data?.ui) {
      return { kind: "continue", flow: resp.data };
    }
    return { kind: "error", message: "Unexpected response from identity provider" };
  } catch (err) {
    const ax = err as AxiosError<KratosFlow>;
    // 400 / 422 with a flow body is the normal "field validation
    // failed" / "csrf_token mismatch" path — surface the flow so the
    // UI can re-render with the per-field errors.
    if (
      (ax.response?.status === 400 || ax.response?.status === 422) &&
      ax.response.data?.ui
    ) {
      return { kind: "continue", flow: ax.response.data };
    }
    if (ax.response?.status === 401 || ax.response?.status === 403) {
      return {
        kind: "error",
        message: "Your session needs re-authentication. Sign out and back in to manage account security.",
      };
    }
    return { kind: "error", message: humanizeKratosError(ax) };
  }
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
 * TOTP enrolment surfaces a QR code image and the base32 secret as
 * non-input nodes. Pull them out so the form can render them above
 * the verification-code field. Returns null when the flow doesn't
 * carry an enrolment payload (already-enrolled users, or non-TOTP
 * flows).
 */
export function totpEnrolmentDisplay(
  flow: KratosFlow,
): { qrSrc?: string; secret?: string } | null {
  let qrSrc: string | undefined;
  let secret: string | undefined;
  for (const node of flow.ui.nodes) {
    if (node.group !== "totp") continue;
    // img + text nodes don't carry an `attributes.name` field — only
    // input nodes do. Filter by node.type + (id|node_type) instead;
    // Kratos returns at most one img + one secret-text per totp
    // group during enrolment, so type alone is enough.
    if (node.type === "img") {
      const attrs = node.attributes as unknown as { src?: string };
      qrSrc = attrs.src ?? qrSrc;
    }
    if (node.type === "text") {
      const attrs = node.attributes as unknown as {
        text?: { text?: string; id?: number };
        id?: string;
      };
      // The base32 secret text node has id "totp_secret_key" or text.id
      // 1050003 in v26. Match either to stay robust across upgrades.
      const looksLikeSecret =
        attrs.id === "totp_secret_key" ||
        attrs.text?.id === 1050003 ||
        // Fallback heuristic: a base32 secret is uppercase A-Z + 2-7
        // and 16+ chars long. Catches version drift without coupling
        // to internal Kratos node ids.
        (typeof attrs.text?.text === "string" &&
          /^[A-Z2-7]{16,}$/.test(attrs.text.text));
      if (looksLikeSecret) {
        secret = attrs.text?.text ?? secret;
      }
    }
  }
  if (!qrSrc && !secret) return null;
  return { qrSrc, secret };
}

/**
 * After regenerating recovery codes, Kratos surfaces the new codes as
 * a `text` node in the `lookup_secret` group. Returns the codes split
 * to an array, or null if the flow doesn't carry them (already-set
 * state or unrelated flow).
 */
export function lookupSecretReveal(flow: KratosFlow): string[] | null {
  for (const node of flow.ui.nodes) {
    if (node.group !== "lookup_secret") continue;
    if (node.type !== "text") continue;
    const attrs = node.attributes as unknown as {
      text?: {
        text?: string;
        id?: number;
        context?: { secrets?: { text?: string }[] };
      };
      id?: string;
    };
    // Match the lookup-secret reveal text by Kratos id 1050015 or
    // attribute id, falling back to "any text node in the
    // lookup_secret group whose context carries a secrets array".
    const looksLikeReveal =
      attrs.id === "lookup_secret_codes" ||
      attrs.text?.id === 1050015 ||
      Array.isArray(attrs.text?.context?.secrets);
    if (!looksLikeReveal) continue;
    const ctx = attrs.text?.context?.secrets;
    if (Array.isArray(ctx)) {
      return ctx.map((s) => s.text ?? "").filter(Boolean);
    }
    if (attrs.text?.text) {
      return attrs.text.text.split(/\s+/).filter(Boolean);
    }
  }
  return null;
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
