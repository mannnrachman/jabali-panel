// M14: Admin Notifications E2E.
//
// Covers the channel CRUD page + topbar bell dropdown. Web Push
// enrolment is browser-native (permission prompt) and cannot be
// scripted cross-vendor — see plans/m14-notifications-runbook.md for
// the manual matrix smoke.
//
// Required env:
//   E2E_BASE_URL   https://jabali-panel.local
//   E2E_USERNAME   admin@example.com
//   E2E_PASSWORD   ...
//
// Auto-skips when any of the above is missing (dev boxes without a
// real panel API).

import { expect, test, type Page } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME / E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `M14 E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M14: Admin Notifications", () => {
    test.setTimeout(120_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";
    const probeName = `e2e-slack-${Date.now()}`;

    async function login(page: Page): Promise<void> {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-(admin|panel)/);
    }

    test("channel CRUD: create slack → toggle disable → delete", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/notifications/channels");
      await expect(page.getByRole("heading", { name: /Notification channels/i })).toBeVisible();

      // Create.
      await page.getByRole("button", { name: /Add channel/i }).click();
      await page.getByLabel(/^Name$/i).fill(probeName);
      // Kind defaults to slack per the drawer's initialValues — no need to open the Select.
      await page.getByLabel(/Webhook URL/i).fill("https://hooks.slack.com/services/TEST/TEST/TEST");
      await page.getByRole("button", { name: /^Create$/i }).click();

      const row = page.getByRole("row", { name: new RegExp(probeName) });
      await expect(row).toBeVisible({ timeout: 10_000 });
      await expect(row.getByText(/slack/i)).toBeVisible();

      // Toggle off via the Switch in the Enabled column.
      await row.locator("button[role=switch]").click();
      // AntD toggles emit a message — wait for it to settle.
      await page.waitForTimeout(400);

      // Delete via the Popconfirm.
      await row.getByRole("button", { name: /^Delete$/i }).click();
      await page.getByRole("button", { name: /^OK$/i }).click();
      await expect(row).not.toBeVisible({ timeout: 10_000 });
    });

    test("bell dropdown: broadcast appears, mark-all-read clears unread badge", async ({
      page,
      request,
    }) => {
      await login(page);

      // Publish a broadcast via the admin API — session cookie is
      // already on the page context; reuse it from `request` against
      // the same baseURL.
      const cookies = await page.context().cookies();
      const cookieHeader = cookies.map((c) => `${c.name}=${c.value}`).join("; ");
      const broadcastTitle = `e2e-broadcast-${Date.now()}`;
      const res = await request.post("/api/v1/admin/notifications/broadcast", {
        headers: { cookie: cookieHeader, "Content-Type": "application/json" },
        data: {
          title: broadcastTitle,
          body: "Playwright smoke test.",
          severity: "info",
        },
      });
      expect(res.status(), await res.text()).toBe(202);

      // Reload so the initial bell fetch includes the new row (the
      // 30s refetch would get there eventually, but this keeps the
      // test snappy).
      await page.goto("/jabali-admin/dashboard");
      const bell = page.getByRole("button", { name: /Notifications/i }).first();
      await expect(bell).toBeVisible();
      // Badge count ≥ 1 — unread badge renders as a superscript number.
      await expect(bell).toContainText(/\d+/);

      // Open the dropdown, assert the broadcast row surfaces.
      await bell.click();
      await expect(page.getByText(broadcastTitle)).toBeVisible();

      // Mark-all-read clears the badge.
      await page.getByRole("button", { name: /Mark all read/i }).click();
      // Close + reopen to force a refetch of the unread count.
      await page.keyboard.press("Escape");
      await page.waitForTimeout(500);
      await bell.click();
      // After mark-all, the AntD Badge 'count' prop is 0 which hides
      // the sup element — assert it's no longer rendered next to the
      // bell icon.
      const supBadge = bell.locator(".ant-badge-count");
      await expect(supBadge).toHaveCount(0);
    });

    test("validation: webhook kind requires >=16-char hmac secret", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/notifications/channels");
      await page.getByRole("button", { name: /Add channel/i }).click();
      await page.getByLabel(/^Name$/i).fill("e2e-bad-webhook");

      // Open Kind Select + choose Generic webhook.
      await page.getByLabel(/^Kind$/i).click();
      await page.getByRole("option", { name: /Generic webhook/i }).click();

      await page.getByLabel(/Target URL/i).fill("https://example.com/hooks/x");
      await page.getByLabel(/HMAC secret/i).fill("tooshort");
      await page.getByRole("button", { name: /^Create$/i }).click();

      // Server 422 surfaces as an AntD error message toast.
      await expect(page.getByText(/hmac_secret/i)).toBeVisible();
    });
  });
}
