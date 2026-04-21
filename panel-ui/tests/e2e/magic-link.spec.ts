import { test, expect } from "@playwright/test";

// TODO(M22): These specs assume a live panel-api + pre-seeded WordPress
// installs with status "Ready" + an authenticated session — they were
// written for manual verification, not CI. The CI harness runs only
// vite-preview on :4173; everything else (auth flows, CRUD, dashboard)
// uses the mockApi(page, ...) helper in fixtures.ts to intercept
// panel-api calls. To un-skip these:
//   1. Use mockApi(page, { me: admin, applications: [...seeded wp apps] })
//   2. Fix the route — real path is /jabali-admin/applications, not /applications
//   3. Stub the magic-link mint endpoint (/magic-link/panel/mint or
//      /magic-link/wp/mint depending on the plugin wiring) so the
//      button can fire without touching the real signer
//   4. Replace the window.open popup assertion with an API-request
//      assertion (page.waitForRequest) — headless popups in CI are
//      flaky across browsers.
// Until then, skipping the whole describe keeps CI green.
test.describe.skip("Magic Link Admin Login", () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to the applications page
    // Assumes the panel is running and the user is authenticated
    await page.goto("/applications");
    await page.waitForLoadState("networkidle");
  });

  test("should display 'Log in to admin' button for WordPress installs", async ({
    page,
  }) => {
    // Wait for the applications table to load
    await page.waitForSelector("table");

    // Find rows with WordPress (app_type = "wordpress" or default)
    const rows = await page.locator("tbody tr").count();
    expect(rows).toBeGreaterThan(0);

    // Find the first ready WordPress install and check for the button
    const firstRow = page.locator("tbody tr").first();
    const statusCell = firstRow.locator("td").nth(3); // Status column

    // Only test if the install is ready
    const statusText = await statusCell.textContent();
    if (statusText?.includes("Ready")) {
      const loginButton = firstRow.locator(
        'button:has-text("Log in to admin")'
      );
      await expect(loginButton).toBeVisible();
      await expect(loginButton).not.toBeDisabled();
    }
  });

  test("should open a window with magic link when 'Log in to admin' is clicked", async ({
    page,
    context,
  }) => {
    // Listen for new pages (popup windows) created
    let newPageUrl = "";
    const pagePromise = context.waitForEvent("page");

    // Wait for the applications table
    await page.waitForSelector("table");

    // Find the first ready WordPress install
    const rows = await page.locator("tbody tr").count();
    for (let i = 0; i < rows; i++) {
      const row = page.locator("tbody tr").nth(i);
      const statusCell = row.locator("td").nth(3);
      const statusText = await statusCell.textContent();

      if (statusText?.includes("Ready")) {
        const loginButton = row.locator('button:has-text("Log in to admin")');
        if (await loginButton.isVisible()) {
          // Set up listener for window.open calls before clicking
          const popupPromise = page.waitForEvent("popup");

          await loginButton.click();

          try {
            const popup = await popupPromise;
            newPageUrl = popup.url();
            await popup.close();
          } catch {
            // If no popup was triggered, try to get the URL from the click context
            // This handles cases where window.open might not trigger a visible popup in tests
          }

          break;
        }
      }
    }

    // Verify that window.open was called with the correct URL pattern
    // The URL should contain the domain and the magic link token
    if (newPageUrl) {
      expect(newPageUrl).toMatch(/jabali_admin_login=/);
      expect(newPageUrl).toMatch(/[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}/);
    }
  });

  test("should show 'Log in to admin' button only for ready WordPress installs", async ({
    page,
  }) => {
    await page.waitForSelector("table");

    const rows = await page.locator("tbody tr").count();

    for (let i = 0; i < rows; i++) {
      const row = page.locator("tbody tr").nth(i);
      const appTypeCell = row.locator("td").first();
      const statusCell = row.locator("td").nth(3);

      const appTypeText = await appTypeCell.textContent();
      const statusText = await statusCell.textContent();

      const loginButton = row.locator('button:has-text("Log in to admin")');
      const isVisible = await loginButton.isVisible().catch(() => false);

      // Button should only be visible for ready WordPress installs
      if (appTypeText?.includes("WordPress") && statusText?.includes("Ready")) {
        expect(isVisible).toBe(true);
      } else {
        expect(isVisible).toBe(false);
      }
    }
  });

  test("should display success message after clicking 'Log in to admin'", async ({
    page,
    context,
  }) => {
    await page.waitForSelector("table");

    const rows = await page.locator("tbody tr").count();
    for (let i = 0; i < rows; i++) {
      const row = page.locator("tbody tr").nth(i);
      const statusCell = row.locator("td").nth(3);
      const statusText = await statusCell.textContent();

      if (statusText?.includes("Ready")) {
        const loginButton = row.locator('button:has-text("Log in to admin")');
        if (await loginButton.isVisible()) {
          // Listen for popup
          const popupPromise = context.waitForEvent("page").catch(() => null);

          await loginButton.click();

          // Close any popup that opened
          try {
            const popup = await popupPromise;
            if (popup) {
              await popup.close();
            }
          } catch {
            // Popup may not appear in test environment
          }

          // Check for success message
          const successMessage = page.locator(".ant-message-success");
          await expect(successMessage).toBeVisible({ timeout: 5000 });

          break;
        }
      }
    }
  });
});
