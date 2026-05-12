// admin-migrations-da-enabled.spec.ts — M35.3: DirectAdmin source
// option is enabled in the CreateMigrationWizard radio group.
// Verifies the wizard accepts directadmin as a draft source_kind +
// the create-draft POST round-trips.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const DRAFT_ID = "01J0DA00WIZDRAFT000000000000";

test.describe("admin migrations — DirectAdmin enabled (M35.3)", () => {
  test("DA radio option is selectable + creates a draft", async ({ page }) => {
    await mockApi(page, { me: admin });

    let draftBody: { source_kind?: string; state?: string } | null = null;
    await page.route("**/api/v1/admin/migrations*", async (route) => {
      const req = route.request();
      if (req.method() === "POST" && !req.url().includes("/bulk")) {
        draftBody = JSON.parse(req.postData() ?? "{}");
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            id: DRAFT_ID,
            batch_id: null,
            source_kind: draftBody!.source_kind,
            source_host: "",
            source_user: "",
            target_user_id: null,
            state: "draft",
            last_error: null,
            started_at: null,
            ended_at: null,
            created_at: "2026-05-12T10:00:00Z",
            updated_at: "2026-05-12T10:00:00Z",
          }),
        });
        return;
      }
      if (req.method() === "GET" && !req.url().includes("/stream")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: [], total: 0, page: 1, page_size: 50 }),
        });
        return;
      }
      await route.fallback();
    });

    await signIn(page, admin);
    await page.goto("/jabali-admin/migrations");

    // Open wizard.
    await page.getByRole("button", { name: "Wizard" }).click();

    // The DA radio should NOT be disabled (M35.3 unblocks it). AntD
    // Radio renders an <input type="radio"> per option — assert by
    // value attribute on the input element.
    const daRadio = page.locator('input[type="radio"][value="directadmin"]');
    await expect(daRadio).toBeVisible({ timeout: 5_000 });
    await expect(daRadio).toBeEnabled();

    await daRadio.check();

    // Continue to next step → triggers POST /admin/migrations with
    // source_kind=directadmin + state=draft.
    await page.getByRole("button", { name: /next: connection/i }).click();

    await expect.poll(() => draftBody?.source_kind, { timeout: 5_000 }).toBe("directadmin");
    expect(draftBody!.state).toBe("draft");
  });

  test("Hestia radio is enabled (M35.4 shipped)", async ({ page }) => {
    await mockApi(page, { me: admin });
    await page.route("**/api/v1/admin/migrations*", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: [], total: 0, page: 1, page_size: 50 }),
        });
        return;
      }
      await route.fallback();
    });

    await signIn(page, admin);
    await page.goto("/jabali-admin/migrations");
    await page.getByRole("button", { name: "Wizard" }).click();

    const hestiaRadio = page.locator('input[type="radio"][value="hestiacp"]');
    await expect(hestiaRadio).toBeVisible({ timeout: 5_000 });
    await expect(hestiaRadio).toBeEnabled();
  });
});
