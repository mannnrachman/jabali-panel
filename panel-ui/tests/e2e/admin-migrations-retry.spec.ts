// admin-migrations-retry.spec.ts — failed-job retry actions
// (M35.1, ADR-0095 decision 7). Both buttons hit POST /:id/retry
// with from_scratch toggled by query string.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const JOB_ID = "01J0FAIL0000000000000000";

test.describe("admin migrations — Retry buttons (M35.1)", () => {
  test("resume + from-scratch fire correct query string", async ({ page }) => {
    await mockApi(page, { me: admin });

    const failedJob = {
      id: JOB_ID,
      batch_id: null,
      source_kind: "cpanel",
      source_host: "src.example.com",
      source_user: "alice",
      target_user_id: null,
      state: "failed",
      manifest_json: null,
      last_error: "validate: schema mismatch",
      started_at: "2026-05-12T10:00:00Z",
      ended_at: "2026-05-12T10:15:00Z",
      created_at: "2026-05-12T09:00:00Z",
      updated_at: "2026-05-12T10:15:00Z",
    };
    const stages = [
      {
        id: "s1", job_id: JOB_ID, stage_name: "analyze", state: "done",
        started_at: "2026-05-12T10:00:00Z", ended_at: "2026-05-12T10:05:00Z",
        bytes_processed: 100, last_error: null,
        created_at: "2026-05-12T10:00:00Z", updated_at: "2026-05-12T10:05:00Z",
      },
      {
        id: "s2", job_id: JOB_ID, stage_name: "validate", state: "failed",
        started_at: "2026-05-12T10:05:00Z", ended_at: "2026-05-12T10:15:00Z",
        bytes_processed: 0, last_error: "schema mismatch",
        created_at: "2026-05-12T10:05:00Z", updated_at: "2026-05-12T10:15:00Z",
      },
    ];

    await page.route(`**/api/v1/admin/migrations/${JOB_ID}`, async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ job: failedJob, stages }),
        });
        return;
      }
      await route.fallback();
    });
    // Block SSE; query falls back to GET.
    await page.route(`**/api/v1/admin/migrations/${JOB_ID}/stream`, async (route) => {
      await route.fulfill({ status: 503, body: "" });
    });

    let retryUrl = "";
    await page.route(`**/api/v1/admin/migrations/${JOB_ID}/retry**`, async (route) => {
      retryUrl = route.request().url();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ job: { ...failedJob, state: "pending" }, from_scratch: retryUrl.includes("from_scratch=true") }),
      });
    });

    await signIn(page, admin);
    await page.goto(`/jabali-admin/migrations/${JOB_ID}`);
    await expect(page.getByText(/migration failed/i).first()).toBeVisible();

    // Resume retry — no query string.
    await page.getByRole("button", { name: /^retry \(resume\)$/i }).click();
    await expect.poll(() => retryUrl).toContain(`/admin/migrations/${JOB_ID}/retry`);
    expect(retryUrl).not.toContain("from_scratch=true");

    // From-scratch retry — query string carries the flag.
    retryUrl = "";
    await page.getByRole("button", { name: /retry from scratch/i }).click();
    await expect.poll(() => retryUrl).toContain("from_scratch=true");
  });
});
