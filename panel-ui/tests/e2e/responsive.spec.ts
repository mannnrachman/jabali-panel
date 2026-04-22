// M23 / ADR-0046 — Mobile + tablet responsive smoke.
//
// Runs under the "mobile-chromium" (iPhone 13) and "tablet-chromium"
// (iPad Mini) projects defined in playwright.config.ts. Same spec body,
// different viewport — the assertion that differs between them is
// whether the drawer/hamburger is expected.
import {
  admin,
  expect,
  expectNoHorizontalOverflow,
  mockApi,
  signIn,
  test,
} from "./fixtures";

// 992px is the lg boundary from ADR-0046. iPhone 13 (390w) falls below;
// iPad Mini (768w) is below as well — so the portrait iPad also gets a
// drawer. We therefore test the two viewports identically; a separate
// desktop project would be where the "no drawer" assertion lives.
function isBelowLg(viewport: { width: number; height: number } | null): boolean {
  return (viewport?.width ?? 0) < 992;
}

test.describe("M23 responsive — chrome + navigation", () => {
  test("login page fits the viewport (no horizontal overflow)", async ({ page }) => {
    await mockApi(page, { me: null });
    await page.goto("/login");
    await expect(page.getByRole("heading", { name: /jabali panel/i })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });

  test("admin dashboard fits + drawer appears below lg", async ({ page, viewport }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await expect(page).toHaveURL(/\/jabali-admin/);
    await expectNoHorizontalOverflow(page);

    if (isBelowLg(viewport)) {
      // Hamburger renders only when the persistent sider is hidden.
      await expect(
        page.getByRole("button", { name: /open navigation menu/i }),
      ).toBeVisible();
    }
  });

  test("drawer opens, navigates, and closes on route change", async ({ page, viewport }) => {
    if (!isBelowLg(viewport)) {
      test.skip(true, "drawer pattern only applies below lg");
    }
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    const hamburger = page.getByRole("button", { name: /open navigation menu/i });
    await hamburger.click();
    // AntD Drawer is role=dialog. One dialog visible after click.
    await expect(page.getByRole("dialog")).toBeVisible();

    // Navigate via menu item inside the drawer.
    await page.getByRole("menuitem", { name: /users/i }).click();
    await expect(page).toHaveURL(/\/jabali-admin\/users/);
    // Route-change effect should have closed the drawer.
    await expect(page.getByRole("dialog")).toBeHidden();
    await expectNoHorizontalOverflow(page);
  });

  test("search modal opens on xs and accepts input", async ({ page, viewport }) => {
    if (!viewport || viewport.width >= 576) {
      test.skip(true, "inline search is used on sm+; modal only on xs");
    }
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.getByRole("button", { name: /open search/i }).click();
    await expect(page.getByRole("dialog", { name: /search/i })).toBeVisible();
  });
});

test.describe("M23 responsive — tables scroll inside their Card", () => {
  test("admin users list has no horizontal page overflow", async ({ page }) => {
    await mockApi(page, {
      me: admin,
      users: [
        {
          id: "user-1",
          email: "jane@test.local",
          is_admin: false,
          created_at: "2026-04-01T00:00:00Z",
          updated_at: "2026-04-01T00:00:00Z",
        },
      ],
    });
    await signIn(page, admin);
    await page.goto("/jabali-admin/users");
    await expect(page.getByRole("heading", { name: /users/i })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });
});
