// M16 Wave E — OAuth 2 / OIDC consent flow E2E.
//
// Exercises the browser half of the per-install WordPress SSO journey
// without standing up Hydra, Kratos, or a live WordPress install.
// What we CAN drive in Playwright:
//
//   - The /consent SPA page behind the Authenticated gate
//   - GET /api/v1/oauth2/consent/:challenge metadata fetch
//   - Allow → POST /oauth2-consent/accept → window.location assign
//   - Deny  → POST /oauth2-consent/deny   → window.location assign
//   - Unauth fallback to /login (Authenticated wrapper behaviour)
//
// What we CAN'T drive at this layer (backend-only; unit tests cover):
//
//   - The trusted-client auto-skip. When a panel-managed install is
//     marked trusted in Hydra, Hydra calls consent-accept on the
//     backend without ever redirecting the browser to /consent — so
//     the SPA is never involved. ADR-0036 Decision 7 + the
//     applications_service.go minting test cover this path.
//
//   - The identity_provider_session_id cascade. Login-accept is
//     backend-only; Decision 5 is verified in oauth2_flow unit tests.
//
// Playwright captures the redirect_to URL by patching
// Object.defineProperty(window.location, 'href', ...) — the same
// technique Consent.test.tsx uses at the unit level. jsdom's
// window.location setter throws "not implemented" otherwise.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const CHALLENGE = "consent-challenge-abc";
const WORDPRESS_REDIRECT = "https://example.com/wp-admin/admin-ajax.php?code=xyz";

// Shape matches panel-api/internal/api/oauth2_flow.go's consent
// metadata response + the Consent.tsx ConsentMetadata type. Scope
// labels come from panel-api/internal/hydraclient/scope_labels.go
// (short/long pair per scope).
function consentMetadata() {
  return {
    client_name: "WordPress @ example.com",
    subject: admin.email,
    requested_scope: [
      { scope: "openid", short: "Verify your identity", long: "Confirms that you are the signed-in user." },
      { scope: "email", short: "See your email address", long: "Lets the app pre-fill your email on forms." },
      { scope: "profile", short: "See your name", long: "Lets the app show your name and basic profile information." },
    ],
  };
}

test.describe("M16 OAuth 2 consent flow", () => {
  test("authenticated user sees consent card and Allow redirects to redirect_to", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    // Intercept the navigation that `window.location.href = redirect_to`
    // triggers. Chromium's native Location object can't be overridden
    // (unlike jsdom), so instead we mock the redirect target URL to
    // serve a sentinel HTML page — then waitForURL proves the SPA
    // actually navigated there.
    await page.route("https://example.com/**", async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "text/html",
        body: `<!doctype html><title>WP callback landed</title><body><h1 data-testid="wp-stub">WP CALLBACK LANDED</h1></body>`,
      });
    });

    // /consent metadata fetch — the page loads this on mount.
    await page.route(`**/api/v1/oauth2/consent/${CHALLENGE}`, async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(consentMetadata()),
      });
    });

    // Capture the Allow POST body to verify grant_scope echoes every
    // requested scope (M16 has no per-scope filter — the Allow button
    // grants the full set).
    let acceptBody: unknown = null;
    await page.route("**/oauth2-consent/accept", async (route) => {
      acceptBody = route.request().postDataJSON();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ redirect_to: WORDPRESS_REDIRECT }),
      });
    });

    await page.goto(`/consent?challenge=${CHALLENGE}`);

    // Metadata surface — client name + every scope short label visible.
    // Client name appears both in the card title ("Authorize …") and
    // in the body paragraph; `.first()` narrows to either.
    await expect(page.getByText("WordPress @ example.com").first()).toBeVisible();
    await expect(page.getByText("Verify your identity")).toBeVisible();
    await expect(page.getByText("See your email address")).toBeVisible();
    await expect(page.getByText("See your name")).toBeVisible();

    // Allow → POST with full grant_scope → redirect_to. The stub HTML
    // we served for example.com renders "WP CALLBACK LANDED", which
    // proves the browser actually followed the redirect rather than
    // staying on /consent with a silently-swallowed error.
    await page.getByRole("button", { name: /allow/i }).click();
    await page.waitForURL(WORDPRESS_REDIRECT);
    await expect(page.getByTestId("wp-stub")).toHaveText("WP CALLBACK LANDED");

    // The grant_scope body must echo the full requested set, in order.
    expect(acceptBody).toEqual({
      challenge: CHALLENGE,
      grant_scope: ["openid", "email", "profile"],
    });
  });

  test("Deny posts to /oauth2-consent/deny and redirects to the returned URL", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    const DENY_REDIRECT = "https://example.com/wp-login.php?oidc_error=denied";

    // Same redirect-stub trick as the Allow test — real Chromium can't
    // have window.location monkey-patched, so we intercept the
    // redirect target and let waitForURL prove the navigation.
    await page.route("https://example.com/**", async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "text/html",
        body: `<!doctype html><title>Denied</title><body><h1 data-testid="deny-stub">DENIED</h1></body>`,
      });
    });

    await page.route(`**/api/v1/oauth2/consent/${CHALLENGE}`, async (route) => {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(consentMetadata()),
      });
    });

    let denyBody: unknown = null;
    await page.route("**/oauth2-consent/deny", async (route) => {
      denyBody = route.request().postDataJSON();
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ redirect_to: DENY_REDIRECT }),
      });
    });

    await page.goto(`/consent?challenge=${CHALLENGE}`);
    await expect(page.getByText("WordPress @ example.com").first()).toBeVisible();

    await page.getByRole("button", { name: /deny/i }).click();
    await page.waitForURL(DENY_REDIRECT);
    await expect(page.getByTestId("deny-stub")).toHaveText("DENIED");

    // Deny body carries the challenge only — no scope list since deny
    // is unconditional.
    expect(denyBody).toEqual({ challenge: CHALLENGE });
  });

  test("unauthenticated visit to /consent bounces to /login", async ({ page }) => {
    // me: null = Kratos whoami returns 401, so the Authenticated gate
    // fails closed.
    await mockApi(page, { me: null });

    // The gated fallback is <CatchAllNavigate to="/login">, so landing
    // on /consent with no session must land on the login page —
    // regardless of the challenge query param.
    await page.goto(`/consent?challenge=${CHALLENGE}`);
    await expect(page).toHaveURL(/\/login/);
  });

  test("404 from metadata fetch surfaces an actionable message", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    await page.route(`**/api/v1/oauth2/consent/${CHALLENGE}`, async (route) => {
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "challenge_not_found" }),
      });
    });

    await page.goto(`/consent?challenge=${CHALLENGE}`);
    // The SPA maps 404 → "expired or been used already" so the user
    // can act (return to the WP login flow and start over) instead of
    // staring at a raw status code.
    await expect(page.getByText(/expired or been used already/i)).toBeVisible();
  });
});
