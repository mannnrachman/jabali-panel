// M10: WordPress install, clone, delete E2E test
//
// This test requires a live jabali-panel deployment and real domain setup.
// It is NOT run in CI (requires E2E_BASE_URL env var and real backend).
//
// To run locally (one-time setup):
// 1. Ensure two domains exist in the panel: E2E_DOMAIN_A and E2E_DOMAIN_B
// 2. Set environment:
//    export E2E_BASE_URL=https://jabali-panel.local
//    export E2E_USERNAME=your-email@example.com
//    export E2E_PASSWORD=your-password
//    export E2E_DOMAIN_A=source-domain.example.com
//    export E2E_DOMAIN_B=clone-destination.example.com
// 3. Run: npm run test:e2e
//
// Flow:
// - Login → Databases page → WordPress page
// - Install WordPress on E2E_DOMAIN_A (creates DB wp_<ulid>)
// - Poll for ready (up to 2min)
// - Visit wp-login.php on live domain → login with admin creds → verify admin dashboard
// - Back to panel → Clone to E2E_DOMAIN_B
// - Poll clone for ready → verify second DB created with different name
// - Visit cloned domain's wp-login.php → verify admin login works with same creds
// - Delete both → confirm rows disappear
//
import { expect, test } from "@playwright/test";

// Skip this entire test if required env vars are missing
const SKIP_REASON =
  !process.env.E2E_DOMAIN_A ||
  !process.env.E2E_DOMAIN_B ||
  !process.env.E2E_USERNAME ||
  !process.env.E2E_PASSWORD
    ? "E2E_BASE_URL, E2E_USERNAME, E2E_PASSWORD, E2E_DOMAIN_A, E2E_DOMAIN_B not all set"
    : "";

if (SKIP_REASON) {
  test.skip(
    () => true,
    `WordPress E2E skipped: ${SKIP_REASON}`,
  );
} else {
  // Only run the actual tests if env vars are present
  test.describe("M10: WordPress install, clone, delete", () => {
    // 5 minutes total timeout for install + clone polling + navigation
    test.setTimeout(300_000);

    // Use HTTPS with cert verification disabled for self-signed panel cert
    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const baseURL = process.env.E2E_BASE_URL || "https://jabali-panel.local";
    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";
    const domainA = process.env.E2E_DOMAIN_A || "";
    const domainB = process.env.E2E_DOMAIN_B || "";

    test("install, clone, delete WordPress end-to-end", async ({ page }) => {
      // --- Login ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in/i }).click();

      // Wait for redirect to panel dashboard
      await page.waitForURL(/\/jabali-panel/);
      await expect(page.getByRole("heading")).toContainText(/dashboard|profile|domains/i);

      // --- Navigate to Databases page (sanity check) ---
      await page.getByRole("link", { name: /databases/i }).click();
      await page.waitForURL(/\/jabali-panel\/databases/);
      await expect(
        page.getByRole("heading", { name: /databases/i }),
      ).toBeVisible();

      // --- Navigate to WordPress page ---
      await page.getByRole("link", { name: /wordpress/i }).click();
      await page.waitForURL(/\/jabali-panel\/wordpress/);
      const wordpressHeading = page.getByRole("heading", { name: /wordpress/i });
      await expect(wordpressHeading).toBeVisible();

      // --- Install on Domain A ---
      // Click Install button to open modal
      const installButton = page.getByRole("button", {
        name: /install|add|new/i,
      });
      await installButton.click();

      // Fill install form
      // The form should have:
      // - domain selection dropdown (or show current domain)
      // - admin username input
      // - admin email input
      // - admin password input
      // - site title input
      // - locale selector
      // - install/submit button

      // Select domain A from dropdown
      await page.getByLabel(/domain/i).click();
      await page.getByRole("option", { name: new RegExp(domainA, "i") }).click();

      // Fill admin details
      const adminUser = "admin";
      const adminEmail = "admin@test.local";
      const adminPass = "TestPassword123!";
      const siteTitle = "Test WordPress Site";

      await page.getByLabel(/admin.*username|username/i).fill(adminUser);
      await page.getByLabel(/admin.*email|email/i).fill(adminEmail);
      await page.getByLabel(/admin.*password|password/i).fill(adminPass);
      await page.getByLabel(/site.*title|title/i).fill(siteTitle);

      // Submit install (button usually "Install", "Create", "Submit")
      await page.getByRole("button", { name: /install|create|submit/i }).click();

      // Modal closes, install starts with status "installing" or "pending"
      await page.waitForURL(/\/jabali-panel\/wordpress/);

      // --- Poll for Domain A "ready" status (up to 2 min) ---
      const installReadyTimeout = 120_000; // 2 minutes
      const pollInterval = 3_000; // 3 seconds
      let startTime = Date.now();

      let domainAReady = false;
      while (!domainAReady && Date.now() - startTime < installReadyTimeout) {
        // Reload page to get latest state
        await page.reload();
        await page.waitForURL(/\/jabali-panel\/wordpress/);

        // Look for a row with domainA and status "ready" or similar
        // The list should show Domain A's install with status indicator
        let rows = await page.getByRole("row").all();
        for (const row of rows) {
          const text = await row.textContent();
          if (text?.includes(domainA) && text?.includes("ready")) {
            domainAReady = true;
            break;
          }
        }

        if (!domainAReady) {
          await page.waitForTimeout(pollInterval);
        }
      }

      expect(domainAReady).toBeTruthy();

      // --- Visit Domain A's wp-login.php and login ---
      // Access the live domain's WordPress login page
      // Bypass cert verification for self-signed certs
      const wpLoginUrl = `https://${domainA}/wp-login.php`;

      // Create a new context with cert bypass for the WordPress site
      const wpContext = await page.context().browser()?.newContext({
        ignoreHTTPSErrors: true,
      });
      expect(wpContext).toBeTruthy();

      const wpPage = await wpContext!.newPage();

      // Navigate to wp-login and login with admin creds
      await wpPage.goto(wpLoginUrl);

      // WordPress login form
      await wpPage.getByLabel(/username|email/i).fill(adminUser);
      await wpPage.getByLabel(/password/i).fill(adminPass);
      await wpPage.getByRole("button", { name: /log in|sign in/i }).click();

      // After login, should see wp-admin dashboard
      // (exact selectors vary; look for common WP dashboard elements)
      await wpPage.waitForURL(/\/wp-admin\//);
      await expect(wpPage.getByText(/dashboard|welcome|wordpress/i)).toBeVisible();

      // Close WP context
      await wpContext!.close();

      // --- Back to panel, clone Domain A to Domain B ---
      await page.bringToFront();
      await page.reload();
      await page.waitForURL(/\/jabali-panel\/wordpress/);

      // Find Domain A's row and click Clone button
      // The clone button should be in the row's action menu
      const rows = await page.getByRole("row").all();
      let cloneButton: any = null;

      for (const row of rows) {
        const text = await row.textContent();
        if (text?.includes(domainA)) {
          // This is Domain A's row; find the clone button or action menu
          // Could be a direct button or inside a dropdown
          const rowCloneBtn = row
            .getByRole("button", { name: /clone/i })
            .first();
          if (await rowCloneBtn.isVisible().catch(() => false)) {
            cloneButton = rowCloneBtn;
            break;
          } else {
            // Try to find action menu button (three dots, etc.)
            const menuBtn = row.getByRole("button", { name: /actions?|menu|more/i }).first();
            if (await menuBtn.isVisible().catch(() => false)) {
              await menuBtn.click();
              // Look for clone in the dropdown
              cloneButton = page.getByRole("menuitem", { name: /clone/i });
              break;
            }
          }
        }
      }

      expect(cloneButton).toBeTruthy();
      await cloneButton.click();

      // Clone modal opens — select Domain B
      // Modal should have a domain selector for the clone destination
      const cloneModal = page.getByRole("dialog");
      await expect(cloneModal).toBeVisible();

      // Select Domain B as the clone destination
      await cloneModal.getByLabel(/destination|target|domain/i).click();
      await page.getByRole("option", { name: new RegExp(domainB, "i") }).click();

      // Submit clone
      await cloneModal
        .getByRole("button", { name: /clone|create|submit/i })
        .click();

      // Modal closes, clone starts
      await page.waitForURL(/\/jabali-panel\/wordpress/);

      // --- Poll for Domain B "ready" status (up to 2 min) ---
      let domainBReady = false;
      const cloneStartTime = Date.now();

      while (
        !domainBReady &&
        Date.now() - cloneStartTime < installReadyTimeout
      ) {
        await page.reload();
        await page.waitForURL(/\/jabali-panel\/wordpress/);

        let rowsForB = await page.getByRole("row").all();
        for (const row of rowsForB) {
          const text = await row.textContent();
          if (text?.includes(domainB) && text?.includes("ready")) {
            domainBReady = true;
            break;
          }
        }

        if (!domainBReady) {
          await page.waitForTimeout(pollInterval);
        }
      }

      expect(domainBReady).toBeTruthy();

      // --- Verify two WP DBs exist with different names ---
      // Go to Databases page
      await page.getByRole("link", { name: /databases/i }).click();
      await page.waitForURL(/\/jabali-panel\/databases/);

      // Look for two DBs matching pattern "wp_*"
      const dbRows = await page.getByRole("row").all();
      const wpDatabases: string[] = [];

      for (const row of dbRows) {
        const text = await row.textContent();
        const match = text?.match(/wp_[a-z0-9]{6,}/);
        if (match) {
          wpDatabases.push(match[0]);
        }
      }

      // Expect exactly 2 WP databases with different names
      expect(wpDatabases.length).toBeGreaterThanOrEqual(2);
      const uniqueDbNames = new Set(wpDatabases);
      expect(uniqueDbNames.size).toBeGreaterThanOrEqual(2);

      // --- Verify clone domain's login works ---
      // Visit Domain B's wp-login.php with same admin creds
      const wpLoginUrlB = `https://${domainB}/wp-login.php`;
      const wpContextB = await page
        .context()
        .browser()
        ?.newContext({
          ignoreHTTPSErrors: true,
        });
      expect(wpContextB).toBeTruthy();

      const wpPageB = await wpContextB!.newPage();
      await wpPageB.goto(wpLoginUrlB);

      // Login with same admin creds (clone preserves them)
      await wpPageB.getByLabel(/username|email/i).fill(adminUser);
      await wpPageB.getByLabel(/password/i).fill(adminPass);
      await wpPageB.getByRole("button", { name: /log in|sign in/i }).click();

      // Should see wp-admin dashboard
      await wpPageB.waitForURL(/\/wp-admin\//);
      await expect(wpPageB.getByText(/dashboard|welcome|wordpress/i)).toBeVisible();

      await wpContextB!.close();

      // --- Delete both installs ---
      await page.bringToFront();
      await page.getByRole("link", { name: /wordpress/i }).click();
      await page.waitForURL(/\/jabali-panel\/wordpress/);

      // Delete Domain A
      let rowsForDelete = await page.getByRole("row").all();
      for (const row of rowsForDelete) {
        const text = await row.textContent();
        if (text?.includes(domainA)) {
          const deleteBtn = row
            .getByRole("button", { name: /delete|remove/i })
            .first();
          if (await deleteBtn.isVisible().catch(() => false)) {
            await deleteBtn.click();
          } else {
            const menuBtn = row
              .getByRole("button", { name: /actions?|menu|more/i })
              .first();
            if (await menuBtn.isVisible().catch(() => false)) {
              await menuBtn.click();
              await page.getByRole("menuitem", { name: /delete|remove/i }).click();
            }
          }
          break;
        }
      }

      // Confirm delete (usually a modal button)
      const deleteConfirmBtn = page.getByRole("button", {
        name: /confirm|delete|yes|remove/i,
      });
      if (await deleteConfirmBtn.isVisible().catch(() => false)) {
        await deleteConfirmBtn.click();
      }

      // Wait a moment for delete to process
      await page.waitForTimeout(2000);

      // Delete Domain B
      rowsForDelete = await page.getByRole("row").all();
      for (const row of rowsForDelete) {
        const text = await row.textContent();
        if (text?.includes(domainB)) {
          const deleteBtn = row
            .getByRole("button", { name: /delete|remove/i })
            .first();
          if (await deleteBtn.isVisible().catch(() => false)) {
            await deleteBtn.click();
          } else {
            const menuBtn = row
              .getByRole("button", { name: /actions?|menu|more/i })
              .first();
            if (await menuBtn.isVisible().catch(() => false)) {
              await menuBtn.click();
              await page.getByRole("menuitem", { name: /delete|remove/i }).click();
            }
          }
          break;
        }
      }

      // Confirm delete
      if (
        await deleteConfirmBtn
          .isVisible()
          .catch(() => false)
      ) {
        await deleteConfirmBtn.click();
      }

      // Wait a moment
      await page.waitForTimeout(2000);

      // --- Verify both rows are gone ---
      await page.reload();
      await page.waitForURL(/\/jabali-panel\/wordpress/);

      let rowsFinal = await page.getByRole("row").all();
      for (const row of rowsFinal) {
        const text = await row.textContent();
        expect(text).not.toContain(domainA);
        expect(text).not.toContain(domainB);
      }
    });
  });
}
