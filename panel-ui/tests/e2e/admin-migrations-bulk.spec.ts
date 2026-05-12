// admin-migrations-bulk.spec.ts — smoke test for the M35.1 bulk WHM
// drawer (POST /admin/migrations/bulk + batch_id surfacing). Mocks
// the backend response; verifies the wizard flow + success surface.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

test.describe("admin migrations — bulk WHM (M35.1)", () => {
  test("operator pastes account list → bulk endpoint fires → success card shows batch_id", async ({ page }) => {
    await mockApi(page, { me: admin });

    // List endpoint: return empty so the page settles fast.
    await page.route("**/api/v1/admin/migrations**", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: [], total: 0, page: 1, page_size: 50 }),
      });
    });

    // Bulk-create mock: capture body, assert source_kind + accounts shape.
    let bulkBody: { source_kind?: string; source_host?: string; accounts?: string[] } | null = null;
    await page.route("**/api/v1/admin/migrations/bulk", async (route) => {
      bulkBody = route.request().postDataJSON();
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          batch_id: "01J0BATCH0000000000000000",
          jobs: [
            { id: "01J0JOB0001", source_user: "alice" },
            { id: "01J0JOB0002", source_user: "bob" },
            { id: "01J0JOB0003", source_user: "charlie" },
          ],
        }),
      });
    });

    await signIn(page, admin);
    await page.goto("/jabali-admin/migrations");

    // Open the bulk drawer.
    await page.getByRole("button", { name: "Bulk WHM" }).click();
    await expect(page.getByText(/create whm migration batch/i).first()).toBeVisible();

    // Fill the form.
    await page.getByPlaceholder("src.example.com").fill("whm.example.com");
    await page.getByPlaceholder(/alice/i).fill("alice\nbob\ncharlie");
    await page.getByRole("button", { name: "Create batch" }).click();

    // Success card should announce the batch_id.
    await expect(page.getByText(/01J0BATCH0000000000000000/).first()).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText(/3 migration_jobs queued/)).toBeVisible();

    // Assert request body.
    expect(bulkBody).not.toBeNull();
    expect(bulkBody!.source_kind).toBe("whm_pkgacct");
    expect(bulkBody!.source_host).toBe("whm.example.com");
    expect(bulkBody!.accounts).toEqual(["alice", "bob", "charlie"]);
  });
});
