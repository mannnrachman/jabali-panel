// M35: Admin Migrations UI E2E.
//
// Smoke tests for the Migrations admin page — read-only happy path.
// Full pipeline E2E (with cPanel/DA/Hestia Docker source fixtures)
// requires a real source host and is deferred to the M35.1 follow-up.
//
// What is covered here:
//   - Page renders at /jabali-admin/migrations
//   - Columns and status chips present
//   - "New Migration" drawer opens with source-kind selector
//   - Empty state visible when no jobs exist
//
// Required env:
//   E2E_BASE_URL  https://jabali-panel.local
//   E2E_USERNAME  admin@example.com
//   E2E_PASSWORD  ...
//
// Skipped automatically when env not provided.

import { expect, test, type Page } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME / E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `M35 E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M35: Admin Migrations", () => {
    test.setTimeout(60_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";

    async function login(page: Page) {
      await page.goto("/auth/login");
      await page.fill('input[name="email"], input[type="email"]', username);
      await page.fill('input[name="password"], input[type="password"]', password);
      await page.click('button[type="submit"]');
      await page.waitForURL(/\/jabali-(admin|panel)/, { timeout: 30_000 });
    }

    test("migrations page renders with table columns", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/migrations");

      // Heading
      await expect(page.getByRole("heading", { name: /Migration/i }).first()).toBeVisible({
        timeout: 15_000,
      });

      // Table columns
      for (const col of ["Source", "User", "Status", "Started"]) {
        await expect(page.getByText(col).first()).toBeVisible();
      }
    });

    test("empty state or existing jobs visible", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/migrations");
      await page.waitForTimeout(2_000);

      // Either a job row OR an empty-state indicator must be present
      const hasRows = await page.locator("table tbody tr").count();
      const hasEmpty = await page.getByText(/No data|No migrations|empty/i).isVisible();
      expect(hasRows > 0 || hasEmpty).toBe(true);
    });

    test("New Migration drawer opens with source-kind step", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/migrations");

      // Open drawer
      const newBtn = page.getByRole("button", { name: /New Migration|New migration|Create/i }).first();
      await expect(newBtn).toBeVisible({ timeout: 10_000 });
      await newBtn.click();

      // Drawer should appear with source-kind selector
      await expect(
        page.getByText(/Source kind|cPanel|DirectAdmin|HestiaCP|WHM/i).first()
      ).toBeVisible({ timeout: 10_000 });
    });

    test("New Migration drawer closes on cancel", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/migrations");

      const newBtn = page.getByRole("button", { name: /New Migration|New migration|Create/i }).first();
      await newBtn.click();

      // Cancel / close
      const cancel = page.getByRole("button", { name: /Cancel|Close/i }).first();
      await cancel.click();

      // Drawer gone
      await expect(
        page.getByText(/Source kind|cPanel|DirectAdmin/i).first()
      ).not.toBeVisible({ timeout: 5_000 });
    });
  });
}
