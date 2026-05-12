// admin-migrations-batch-cancel.spec.ts — operator clicks the
// batch-id Tag on the list page, confirms the Popconfirm, and the
// page hits DELETE /admin/migrations/batches/:id (M35.1, ADR-0095
// decision 3 cancel-batch path).
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const BATCH_ID = "01J0BATCHCANCEL000000000000";

test.describe("admin migrations — cancel batch (M35.1)", () => {
  test("click batch tag → confirm → DELETE /batches/:id fires", async ({ page }) => {
    await mockApi(page, { me: admin });

    const baseRow = (id: string, user: string) => ({
      id,
      batch_id: BATCH_ID,
      source_kind: "whm_pkgacct",
      source_host: "whm.example.com",
      source_user: user,
      target_user_id: null,
      state: "pending",
      last_error: null,
      started_at: "2026-05-12T10:00:00Z",
      ended_at: null,
      created_at: "2026-05-12T10:00:00Z",
      updated_at: "2026-05-12T10:00:00Z",
    });

    // GET /admin/migrations — return two jobs sharing the batch_id.
    await page.route("**/api/v1/admin/migrations*", async (route) => {
      const req = route.request();
      if (req.method() === "GET" && !req.url().includes("/batches/") && !req.url().includes("/stream")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            data: [baseRow("job-alice", "alice"), baseRow("job-bob", "bob")],
            total: 2,
            page: 1,
            page_size: 50,
          }),
        });
        return;
      }
      await route.fallback();
    });

    // DELETE /admin/migrations/batches/:id — capture URL.
    let deleteUrl = "";
    await page.route(`**/api/v1/admin/migrations/batches/${BATCH_ID}`, async (route) => {
      if (route.request().method() === "DELETE") {
        deleteUrl = route.request().url();
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ batch_id: BATCH_ID, cancelled: 2, total: 2 }),
        });
        return;
      }
      await route.fallback();
    });

    await signIn(page, admin);
    await page.goto("/jabali-admin/migrations");

    // Wait for the two rows to render — assert by source_user code text.
    await expect(page.getByText("alice").first()).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText("bob").first()).toBeVisible();

    // The batch Tag shows the last 6 chars of the batch_id (M35.1
    // list view). Both rows share the same batch, so two tags render.
    const tag = page.getByText(BATCH_ID.slice(-6)).first();
    await expect(tag).toBeVisible();

    // Click the tag → Popconfirm appears → click "Cancel batch".
    await tag.click();
    const confirmBtn = page.getByRole("button", { name: /^cancel batch$/i });
    await expect(confirmBtn).toBeVisible({ timeout: 5_000 });
    await confirmBtn.click();

    await expect.poll(() => deleteUrl).toContain(`/admin/migrations/batches/${BATCH_ID}`);
  });
});
