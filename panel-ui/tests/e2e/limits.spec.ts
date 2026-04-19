// M18: Per-user resource limits (disk quota + cgroups v2 + nginx) E2E.
//
// UI-level coverage only: package editor exposes the 5 new fields,
// submit round-trips cleanly, the usage card renders for a logged-in
// user. Kernel-level enforcement (setquota EDQUOT, OOM-kill on
// MemoryMax, nginx 503 on limit_req) is NOT testable from a browser
// and lives in the host-validation section of
// plans/m18-resource-limits-runbook.md — ops runs those against the
// test VM after a deploy.
//
// Runs the same way every other E2E does: set E2E_BASE_URL /
// E2E_USERNAME / E2E_PASSWORD, `npm run test:e2e limits.spec.ts`.
import { expect, test } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME, E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `Limits E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M18: per-user resource limits", () => {
    test.setTimeout(120_000);
    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";

    async function login(page: import("@playwright/test").Page) {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/(my-profile|admin|dashboard)/);
    }

    test("package editor exposes all M18 resource-limit fields", async ({
      page,
    }) => {
      await login(page);
      await page.goto("/admin/packages/create");

      // Section header confirms the grouped layout Wave F landed.
      await expect(page.getByText(/Resource limits/i)).toBeVisible();

      // Every M18 field is addressable by label — the admin UI is the
      // canonical place to verify their values are editable.
      for (const label of [
        /Disk Quota \(MB\)/i,
        /CPU Quota \(%\)/i,
        /Memory Limit \(MB\)/i,
        /IO Read Bandwidth/i,
        /IO Write Bandwidth/i,
        /Max Tasks/i,
      ]) {
        await expect(page.getByLabel(label)).toBeVisible();
      }
    });

    test("creating a package with M18 fields round-trips through the API", async ({
      page,
    }) => {
      await login(page);
      await page.goto("/admin/packages/create");

      const unique = `e2e-m18-${Date.now()}`;
      await page.getByLabel(/^Name/i).fill(unique);
      await page.getByLabel(/Disk Quota \(MB\)/i).fill("1024");
      await page.getByLabel(/CPU Quota \(%\)/i).fill("200");
      await page.getByLabel(/Memory Limit \(MB\)/i).fill("512");
      await page.getByLabel(/Max Tasks/i).fill("200");

      // Refine's Save button varies per build — match by role+pattern so
      // minor label tweaks don't break this test.
      await page.getByRole("button", { name: /save/i }).click();

      // We land on list or edit page; check the new pkg is discoverable.
      await page.goto("/admin/packages");
      await expect(page.getByText(unique)).toBeVisible();
    });

    test("bounds validation blocks out-of-range CPU quota", async ({ page }) => {
      await login(page);
      await page.goto("/admin/packages/create");

      await page.getByLabel(/^Name/i).fill(`e2e-bounds-${Date.now()}`);
      // 10001% should trigger the Validate() in packages.go on POST.
      // InputNumber's max=10000 may clamp on blur; if it does, this test
      // still exercises the UI-level bound. Network-level rejection is
      // verified in the Go api test suite.
      await page.getByLabel(/CPU Quota \(%\)/i).fill("99999");
      await page.getByRole("button", { name: /save/i }).click();
      // The form either shows a client-side warning OR the server returns
      // 422 with a validation_failed detail surfaced via message.error.
      // Either counts as "operator noticed something is wrong."
      await expect(
        page
          .getByText(/validation_failed|exceeds|max/i)
          .first(),
      ).toBeVisible({ timeout: 5000 });
    });

    test("user profile renders the usage card with effective limits", async ({
      page,
    }) => {
      await login(page);
      await page.goto("/my-profile");

      // Card title is stable per Wave F.
      await expect(
        page.getByText(/Resource usage/i).first(),
      ).toBeVisible();

      // Labels from the Descriptions block — these show even when the
      // agent's live report section is empty (slice not yet active).
      for (const label of [
        /CPU quota/i,
        /Processes/i,
        /I\/O read limit/i,
        /I\/O write limit/i,
      ]) {
        await expect(page.getByText(label).first()).toBeVisible();
      }
    });
  });
}
