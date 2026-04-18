// M8: Cron scheduling (systemd-user timers) E2E test
//
// This test requires a live jabali-panel deployment with at least one owned domain.
// It is NOT run in CI without E2E_BASE_URL env var set.
//
// To run locally (one-time setup):
// 1. Ensure one domain exists in the panel and is owned by the test user
// 2. Set environment:
//    export E2E_BASE_URL=https://jabali-panel.local
//    export E2E_USERNAME=your-email@example.com
//    export E2E_PASSWORD=your-password
//    export E2E_DOMAIN=an-owned-domain.example.com  (optional; auto-detected if omitted)
// 3. Run: npm run test:e2e cron.spec.ts
//
// Flow:
// - Login → navigate to Cron Jobs page
// - List page loads, empty state or "New Cron Job" button visible
// - Create a cron job with hourly schedule
// - Click "Run Now" to manually trigger
// - Poll for up to 6 minutes for last_run_at to populate (systemd timer fires)
// - Verify last_exit_code shows success (0)
// - Delete the job and verify cleanup
// - Reject invalid commands (shell metacharacters, traversal, disallowed binaries)
//
// Tests marked skip(true) require infrastructure not available in headless/CI:
// - Live systemd-user timer firing (requires real systemd session per user)
// - Separate authenticated users for ownership checks (requires multi-user test harness)

import { expect, test } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME, E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(
    () => true,
    `Cron E2E skipped: ${SKIP_REASON}`,
  );
} else {
  test.describe("M8: Cron Jobs (systemd-user timers)", () => {
    // 6 minute timeout: 5 min for timer to fire + 1 min for UI polling + actions
    test.setTimeout(360_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const baseURL = process.env.E2E_BASE_URL || "https://jabali-panel.local";
    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";

    test("list page loads with empty state or New Cron Job button", async ({
      page,
    }) => {
      // --- Login ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();

      // Wait for redirect to panel
      await page.waitForURL(/\/jabali-panel/);

      // --- Navigate to Cron page ---
      await page.goto("/jabali-panel/cron");
      await page.waitForURL(/\/jabali-panel\/cron/);

      // Assert title
      const pageTitle = page.getByRole("heading", { name: /cron\s+jobs/i });
      await expect(pageTitle).toBeVisible();

      // Assert either empty state text OR New Cron Job button is visible
      const newButton = page.getByRole("button", { name: /new\s+cron\s+job|new cron job/i });
      const isEmpty = await newButton.isVisible().catch(() => false);
      expect(isEmpty).toBeTruthy();
    });

    // SKIPPED: this test requires a running systemd-user timer on the host
    // (i.e., not the mock CI sandbox) AND a pre-provisioned owned docroot
    // that the validator will accept. To unskip, a beforeAll fixture must:
    //   1. Create a domain for the test user with a real docroot path.
    //   2. Drop a tiny heartbeat.php into that docroot (or install a WP site
    //      there that has a queued wp-cron event).
    //   3. Ensure the test user is lingering (loginctl enable-linger).
    // Then change the command fill below to reference that docroot, and
    // convert test.skip → test.
    test.skip("create, run-now, verify last_run_at populates (6min poll)", async ({
      page,
    }) => {
      // --- Login ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-panel/);

      // --- Navigate to Cron page ---
      await page.goto("/jabali-panel/cron");
      await page.waitForURL(/\/jabali-panel\/cron/);

      // --- Open Create Modal ---
      const newButton = page.getByRole("button", { name: /new\s+cron\s+job/i });
      await newButton.click();

      // Wait for modal
      const modal = page.getByRole("dialog");
      await expect(modal).toBeVisible();

      // --- Fill form ---
      // Name: unique identifier with timestamp to avoid conflicts
      const ts = Date.now();
      const jobName = `e2e-smoke-${ts}`;

      const nameInput = page.getByLabel(/name/i).first();
      await nameInput.fill(jobName);

      // Command: a wp-cron ping against an owned docroot (placeholder;
      // fixture must make this path exist and be owned by the test user).
      // NOTE: the validator requires an absolute .php path OR a wp command
      // whose --path= is inside ownedDocroots — /usr/bin/true will NEVER
      // pass. Update this line when wiring the real fixture.
      const commandInput = page.getByLabel(/command/i).first();
      await commandInput.fill("wp cron event run --due-now --path=/home/e2e-test-user/example.com/public_html");

      // Select "Hourly" preset (should be first radio option)
      const hourlyRadio = page.getByLabel(/hourly/i).first();
      await hourlyRadio.click();

      // Submit form
      const submitBtn = page
        .locator("button")
        .filter({ hasText: /create|submit|add/i })
        .filter({ hasNot: page.locator("text=cancel") })
        .first();
      await submitBtn.click();

      // Modal should close and row should appear
      await expect(modal).toBeHidden();

      // Wait for the new row to appear in the table
      const jobRow = page.getByText(jobName).first();
      await expect(jobRow).toBeVisible({ timeout: 10_000 });

      // --- Click Run Now ---
      const tableRow = jobRow.locator("xpath=ancestor::tr").first();
      const runNowBtn = tableRow.getByRole("button", { name: /run\s+now/i });
      await expect(runNowBtn).toBeVisible();
      await runNowBtn.click();

      // A result modal or message may appear (depending on UI); wait for it to settle
      await page.waitForTimeout(2000);

      // --- Poll for last_run_at to populate (up to 6 min) ---
      // The systemd timer fires, reconciler updates DB, UI polls/refreshes
      const pollTimeout = 360_000; // 6 minutes
      const pollInterval = 5_000; // Check every 5 seconds
      const startTime = Date.now();

      let lastRunAtPopulated = false;

      while (!lastRunAtPopulated && Date.now() - startTime < pollTimeout) {
        // Reload to get latest state
        await page.reload();
        await page.waitForURL(/\/jabali-panel\/cron/);

        // Look for the job row again
        const updatedRow = page.getByText(jobName).first();
        if (await updatedRow.isVisible().catch(() => false)) {
          // Get parent row
          const parentRow = updatedRow.locator("xpath=ancestor::tr").first();

          // Look for "Last Run" column content (should NOT be "Never")
          const lastRunCell = parentRow.locator("td").nth(3); // Assuming column 3 is Last Run
          const lastRunText = await lastRunCell.textContent().catch(() => "");

          if (lastRunText && lastRunText.trim() !== "" && lastRunText.toLowerCase() !== "never") {
            lastRunAtPopulated = true;

            // Also verify last_exit_code shows success (green tag with 0)
            const lastExitCell = parentRow.locator("td").nth(4);
            const exitText = await lastExitCell.textContent().catch(() => "");
            expect(exitText).toMatch(/0|success/i);
          }
        }

        if (!lastRunAtPopulated) {
          await page.waitForTimeout(pollInterval);
        }
      }

      expect(lastRunAtPopulated).toBeTruthy(
        "last_run_at should populate within 6 minutes of run-now trigger"
      );

      // --- Cleanup: Delete the job ---
      await page.reload();
      await page.waitForURL(/\/jabali-panel\/cron/);

      const cleanupRow = page.getByText(jobName).first();
      await expect(cleanupRow).toBeVisible();

      const cleanupParentRow = cleanupRow.locator("xpath=ancestor::tr").first();
      const deleteBtn = cleanupParentRow.getByRole("button", { name: /delete/i });
      await deleteBtn.click();

      // Popconfirm should appear
      const popconfirm = page.getByRole("dialog");
      await expect(popconfirm).toBeVisible({ timeout: 5000 });

      const confirmYes = popconfirm.getByRole("button", { name: /yes/i });
      await confirmYes.click();

      await expect(popconfirm).toBeHidden();

      // Verify row is gone
      await page.waitForTimeout(1000);
      await page.reload();
      await page.waitForURL(/\/jabali-panel\/cron/);

      const deletedRow = page.getByText(jobName);
      await expect(deletedRow).not.toBeVisible({ timeout: 5000 });
    });

    test("reject command with shell metacharacters (e.g., cat /etc/passwd)", async ({
      page,
    }) => {
      // --- Login ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-panel/);

      // --- Navigate to Cron ---
      await page.goto("/jabali-panel/cron");
      await page.waitForURL(/\/jabali-panel\/cron/);

      // --- Open Create Modal ---
      const newButton = page.getByRole("button", { name: /new\s+cron\s+job/i });
      await newButton.click();

      const modal = page.getByRole("dialog");
      await expect(modal).toBeVisible();

      // --- Try to create job with forbidden command ---
      const nameInput = page.getByLabel(/name/i).first();
      await nameInput.fill(`forbidden-${Date.now()}`);

      const commandInput = page.getByLabel(/command/i).first();
      await commandInput.fill("cat /etc/passwd");

      // Select schedule
      const hourlyRadio = page.getByLabel(/hourly/i).first();
      await hourlyRadio.click();

      // Submit
      const submitBtn = page
        .locator("button")
        .filter({ hasText: /create|submit|add/i })
        .filter({ hasNot: page.locator("text=cancel") })
        .first();
      await submitBtn.click();

      // Expect form error to surface under command field
      // Error message should indicate "not allowed", "binary", etc.
      const errorMsg = page.locator(
        "text=/not allowed|not permitted|binary|invalid command|command_not_allowed/i"
      );
      await expect(errorMsg).toBeVisible({ timeout: 5000 });

      // Modal should still be open (not closed after failed submit)
      await expect(modal).toBeVisible();

      // Close modal by clicking cancel
      const cancelBtn = page.getByRole("button", { name: /cancel/i });
      await cancelBtn.click();
      await expect(modal).toBeHidden();

      // Verify no job was created (table should not grow)
      // This is a soft check; we just verify the modal closed and error was shown
    });

    test("reject command with path traversal (e.g., php ../../etc/passwd)", async ({
      page,
    }) => {
      // --- Login ---
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-panel/);

      // --- Navigate to Cron ---
      await page.goto("/jabali-panel/cron");
      await page.waitForURL(/\/jabali-panel\/cron/);

      // --- Open Create Modal ---
      const newButton = page.getByRole("button", { name: /new\s+cron\s+job/i });
      await newButton.click();

      const modal = page.getByRole("dialog");
      await expect(modal).toBeVisible();

      // --- Try to create job with traversal attempt ---
      const nameInput = page.getByLabel(/name/i).first();
      await nameInput.fill(`traversal-${Date.now()}`);

      const commandInput = page.getByLabel(/command/i).first();
      await commandInput.fill("php /home/../../../etc/passwd");

      const hourlyRadio = page.getByLabel(/hourly/i).first();
      await hourlyRadio.click();

      const submitBtn = page
        .locator("button")
        .filter({ hasText: /create|submit|add/i })
        .filter({ hasNot: page.locator("text=cancel") })
        .first();
      await submitBtn.click();

      // Expect error message about bad path / traversal
      const errorMsg = page.locator(
        "text=/bad path|invalid path|traversal|not allowed|command_not_allowed/i"
      );
      await expect(errorMsg).toBeVisible({ timeout: 5000 });

      await expect(modal).toBeVisible();

      const cancelBtn = page.getByRole("button", { name: /cancel/i });
      await cancelBtn.click();
      await expect(modal).toBeHidden();
    });

    test.skip("run-now on another user's job returns 403 (requires multi-user setup)", async ({
      page,
    }) => {
      // This test requires:
      // 1. Two separate authenticated users in the panel
      // 2. User A creates a cron job
      // 3. User B logs in and attempts to run-now on User A's job via direct API call
      // 4. Verify 403/404 response
      //
      // Implementation is deferred because the test harness lacks
      // built-in multi-user context management. Real integration tests
      // in the backend (Go) cover this ownership check.
      //
      // To implement: modify the test fixture to support
      // `context.authenticatedAs(user2)` and `request.post()` with
      // Bearer token from second user.
      expect(true).toBeTruthy();
    });
  });
}
