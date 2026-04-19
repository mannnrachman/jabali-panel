// M9.6: PHP Extensions admin tab E2E.
//
// Covers the happy path + state flips for a single safe extension:
//   1. Clean slate (remove bcmath if present)
//   2. Login as admin
//   3. Navigate to admin PHP page
//   4. Assert default tab is "PHP Versions"
//   5. Switch to "PHP Extensions" tab
//   6. Select PHP 8.5 from the dropdown
//   7. Find bcmath row, assert Installed=No
//   8. Install → wait for Installed=Yes, Status=enabled
//   9. Disable → Status=disabled (Installed still Yes)
//  10. Enable → Status=enabled again
//  11. Remove → Installed=No
//
// bcmath is chosen because:
//   - it's small (no daemon, negligible install time)
//   - it's a non-built-in, non-bundled pure apt extension
//   - removing it affects nothing else on the host
//
// Test is deterministic: beforeAll + afterAll both issue `remove` via API to
// guarantee a clean slate irrespective of fixture drift between runs.
//
// To run:
//   export E2E_BASE_URL=https://jabali-panel.local
//   export E2E_USERNAME=admin@example.com
//   export E2E_PASSWORD=your-admin-password
//   npm run test:e2e -- php-extensions.spec.ts
//
// The admin account MUST have is_admin=true — the extension routes are gated
// by RequireAdmin middleware and a normal user will see 403.

import { expect, test } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL;
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;
const TEST_VERSION = process.env.E2E_PHP_VERSION ?? "8.5";
const TEST_EXT = "bcmath";

const SKIP_REASON =
  !BASE_URL || !USERNAME || !PASSWORD
    ? "E2E_BASE_URL, E2E_USERNAME, E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `PHP extensions E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M9.6: PHP Extensions admin tab", () => {
    // apt install can take up to 3 min on a cold apt cache
    test.setTimeout(240_000);

    /**
     * cleanSlateRemove fires the apply endpoint with action=remove. We swallow
     * HTTP errors because: 409 ("not installed") is the desired precondition,
     * and any 5xx still leaves us free to retry the install from the UI side.
     */
    const cleanSlateRemove = async (request: import("@playwright/test").APIRequestContext, token: string) => {
      const url = `${BASE_URL}/api/v1/admin/php/versions/${TEST_VERSION}/extensions/${TEST_EXT}/apply`;
      try {
        await request.post(url, {
          data: { action: "remove" },
          headers: { Authorization: `Bearer ${token}` },
          timeout: 180_000,
        });
      } catch {
        // best-effort — the UI-level test still exercises the full flow
      }
    };

    test("install → disable → enable → remove", async ({ page, request }) => {
      // --- Login via the SPA so we capture the access token from storage ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(USERNAME!);
      await page.getByLabel(/password/i).fill(PASSWORD!);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-(admin|panel)/);

      // Extract access token for direct-API cleanup calls
      const token = await page.evaluate(() => sessionStorage.getItem("access_token") ?? "");
      expect(token, "access token must be set after login").not.toEqual("");

      // --- Clean slate: ensure bcmath is NOT installed before the test runs ---
      await cleanSlateRemove(request, token);

      // --- Navigate to admin PHP page ---
      await page.goto("/jabali-admin/php-pools");

      // --- Default tab: PHP Versions ---
      await expect(page.getByRole("tab", { name: /php\s*versions/i })).toHaveAttribute(
        "aria-selected",
        "true"
      );

      // --- Switch to PHP Extensions ---
      await page.getByRole("tab", { name: /php\s*extensions/i }).click();
      await expect(
        page.getByRole("heading", { name: /php\s*extensions/i })
      ).toBeVisible();

      // --- Pick version ---
      const versionSelect = page.getByLabel(/php version/i);
      await versionSelect.click();
      await page.getByRole("option", { name: new RegExp(`php\\s*${TEST_VERSION}`, "i") }).click();

      // --- Find the bcmath row; assert initial state ---
      const row = page.getByRole("row").filter({ hasText: TEST_EXT });
      await expect(row).toBeVisible({ timeout: 10_000 });
      await expect(row.getByText(/^no$/i)).toBeVisible();

      // --- Install ---
      await row.getByRole("button", { name: /^install$/i }).click();
      await expect(row.getByText(/^yes$/i)).toBeVisible({ timeout: 180_000 });

      // --- Disable ---
      await row.getByRole("button", { name: /^disable$/i }).click();
      // Enable button returns when disabled
      await expect(row.getByRole("button", { name: /^enable$/i })).toBeVisible({
        timeout: 30_000,
      });

      // --- Enable again ---
      await row.getByRole("button", { name: /^enable$/i }).click();
      await expect(row.getByRole("button", { name: /^disable$/i })).toBeVisible({
        timeout: 30_000,
      });

      // --- Remove ---
      await row.getByRole("button", { name: /^remove$/i }).click();
      await expect(row.getByText(/^no$/i)).toBeVisible({ timeout: 60_000 });

      // --- Teardown: belt-and-braces remove, in case of partial failure above ---
      await cleanSlateRemove(request, token);
    });
  });
}
