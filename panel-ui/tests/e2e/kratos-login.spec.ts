// M20 Kratos login flow E2E — exercises the Ory Kratos browser self-service
// integration without requiring a live Kratos instance. Routes /.ory/* are
// mocked via mockApi() in fixtures.ts so this spec focuses on Kratos-specific
// behaviour — CSRF token round-trip, session-cookie attach, flow id rehydrate.
//
// Complements panel-ui/src/pages/Login.test.tsx (which covers the same
// flow progression at the unit level against mocked axios).
import { admin, expect, mockApi, signIn, test, user } from "./fixtures";

test.describe("Kratos login flow", () => {
  test("admin signs in via /.ory self-service flow and lands on /jabali-admin", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await expect(page).toHaveURL(/\/jabali-admin/);
  });

  test("non-admin signs in and lands on /jabali-panel", async ({ page }) => {
    await mockApi(page, { me: user });
    await signIn(page, user);
    await expect(page).toHaveURL(/\/jabali-panel/);
  });

  test("CSRF token from the flow is forwarded on submit", async ({ page }) => {
    await mockApi(page, { me: admin });

    // Intercept the submit POST BEFORE signIn so the route-match wins over
    // the fixtures.ts handler. Assert the form body includes the exact
    // csrf_token value emitted by kratosPasswordFlow().
    let submittedCSRF: string | null = null;
    await page.route("**/.ory/self-service/login?flow=*", async (route) => {
      const req = route.request();
      const body = req.postData() ?? "";
      const match = body.match(/csrf_token=([^&]+)/);
      if (match) submittedCSRF = decodeURIComponent(match[1]);
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        headers: {
          "Set-Cookie": "ory_kratos_session=mock-session; Path=/; HttpOnly; SameSite=Lax",
        },
        body: JSON.stringify({
          session: {
            id: "session-admin",
            identity: {
              id: "kratos-admin",
              schema_id: "default",
              traits: { email: admin.email, is_admin: true },
            },
          },
        }),
      });
    });

    await signIn(page, admin);

    await expect(page).toHaveURL(/\/jabali-admin/);
    // Must have forwarded the flow's csrf_token — missing it is the top
    // Kratos integration bug and the whole reason we render ui.nodes.
    expect(submittedCSRF).toBe("csrf-token-xyz");
  });

  test("whoami 401 on a cold load keeps us on /login", async ({ page }) => {
    // No mockApi here — fixtures install whoami=401 when state.session is null.
    // The SPA should treat this as "unauthenticated" and redirect to /login.
    await mockApi(page, { me: null });
    await page.goto("/jabali-admin/dashboard");
    await expect(page).toHaveURL(/\/login$/);
  });

  test("logout clears ory_kratos_session cookie and redirects to /login", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await expect(page).toHaveURL(/\/jabali-admin/);

    // Verify the session cookie is present after login.
    let cookies = await page.context().cookies();
    const sessionCookieBeforeLogout = cookies.find((c) => c.name === "ory_kratos_session");
    expect(sessionCookieBeforeLogout).toBeDefined();
    expect(sessionCookieBeforeLogout?.value).toBe("mock-session");

    // Trigger logout via the user dropdown "Sign out" button.
    // JabaliHeader renders: Dropdown menu with key="logout", label="Sign out".
    await page.getByRole("button", { name: /admin@test\.local/ }).click();
    await page.getByRole("menuitem", { name: /sign out/i }).click();

    // After logout, the browser should be redirected to /login.
    await expect(page).toHaveURL(/\/login$/);

    // Verify the session cookie is gone.
    cookies = await page.context().cookies();
    const sessionCookieAfterLogout = cookies.find((c) => c.name === "ory_kratos_session");
    expect(sessionCookieAfterLogout).toBeUndefined();

    // Sanity check: navigate to a protected route; should bounce back to /login
    // because the session is dead, not just the cookie stale.
    await page.goto("/jabali-admin/dashboard");
    await expect(page).toHaveURL(/\/login$/);
  });
});
