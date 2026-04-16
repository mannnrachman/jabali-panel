// Auth + RoleGate E2E — the minimum that proves total-separation works
// from a user's point of view (i.e. the URL bar).
import { admin, expect, mockApi, signIn, test, user } from "./fixtures";

test.describe("login + role-based landing", () => {
  test("admin lands on /jabali-admin after signing in", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    await expect(page).toHaveURL(/\/jabali-admin\/(dashboard|users)(\?|$)/);
    await expect(
      page.getByRole("heading", { name: /dashboard|users/i }),
    ).toBeVisible();
  });

  test("non-admin lands on /jabali-panel after signing in", async ({ page }) => {
    await mockApi(page, { me: user });
    await signIn(page, user);

    await expect(page).toHaveURL(/\/jabali-panel(\/profile)?$/);
    await expect(page.getByRole("heading", { name: /my profile/i })).toBeVisible();
  });

  test("wrong password stays on /login and shows an error", async ({ page }) => {
    await mockApi(page, { me: admin });

    await page.goto("/login");
    await page.getByLabel(/email/i).fill("nobody@example.com");
    await page.getByLabel(/password/i).fill("whatever");
    await page.getByRole("button", { name: /sign in/i }).click();

    await expect(page).toHaveURL(/\/login$/);
    // AntD's notification surfaces the auth error; look for the text
    // the authProvider emits.
    await expect(page.getByText(/incorrect email or password/i)).toBeVisible();
  });
});

test.describe("RoleGate cross-shell blocks", () => {
  test("admin visiting /jabali-panel is redirected back to /jabali-admin", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);

    await page.goto("/jabali-panel/profile");

    await expect(page).toHaveURL(/\/jabali-admin/);
  });

  test("non-admin visiting /jabali-admin/users is redirected to /jabali-panel", async ({ page }) => {
    await mockApi(page, { me: user });
    await signIn(page, user);
    await page.waitForURL(/\/jabali-panel/);

    await page.goto("/jabali-admin/users");

    await expect(page).toHaveURL(/\/jabali-panel/);
  });
});

test.describe("logout", () => {
  test("signing out returns to /login", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);

    // The "Welcome back" AntD notification from login intercepts pointer
    // events if we click too soon. Wait for it to auto-close (~4.5s).
    await page.locator(".ant-notification").waitFor({ state: "hidden", timeout: 10_000 });

    // Open the user dropdown → click "Sign out". The dropdown trigger is
    // the button that shows the logged-in email in the header.
    await page.getByRole("button", { name: new RegExp(admin.email, "i") }).click();
    await page.getByRole("menuitem", { name: /sign out/i }).click();

    await expect(page).toHaveURL(/\/login$/);
  });
});
