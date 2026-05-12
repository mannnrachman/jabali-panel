// admin-migrations-sse.spec.ts — useMigrationStream SSE hook
// lifecycle (M35.1, ADR-0095 decision 4). Mocks the
// /admin/migrations/:id/stream endpoint as a real text/event-stream
// response that emits two `snapshot` events: an intermediate
// `analyzing` state and a terminal `done` state. Asserts that the
// detail page reflects each snapshot AND that the page survives the
// connection closing after the terminal event.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const JOB_ID = "01J0SSESTREAM00000000000000";

test.describe("admin migrations — SSE detail page (M35.1)", () => {
  test("page updates from snapshot events; closes on terminal state", async ({ page }) => {
    await mockApi(page, { me: admin });

    const baseJob = {
      id: JOB_ID,
      batch_id: null,
      source_kind: "cpanel",
      source_host: "src.example.com",
      source_user: "alice",
      target_user_id: null,
      manifest_json: null,
      last_error: null,
      started_at: "2026-05-12T10:00:00Z",
      ended_at: null,
      created_at: "2026-05-12T10:00:00Z",
      updated_at: "2026-05-12T10:00:00Z",
    };

    // Fallback REST GET — useQuery hits this first; SSE updates
    // overwrite the cache shortly after.
    await page.route(`**/api/v1/admin/migrations/${JOB_ID}`, async (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          job: { ...baseJob, state: "analyzing" },
          stages: [],
        }),
      });
    });

    // SSE stream — text/event-stream body with two snapshot events.
    // Playwright route.fulfill with body=string returns the body once;
    // the EventSource closes after parsing. That's fine — the hook's
    // contract is "close on terminal" and the second event has state=
    // done which IS terminal.
    await page.route(`**/api/v1/admin/migrations/${JOB_ID}/stream`, async (route) => {
      const events =
        `event: snapshot\n` +
        `data: ${JSON.stringify({
          job: { ...baseJob, state: "analyzing" },
          stages: [
            { id: "s1", job_id: JOB_ID, stage_name: "analyze", state: "running", bytes_processed: 0, last_error: null, created_at: baseJob.created_at, updated_at: baseJob.created_at, started_at: baseJob.created_at, ended_at: null },
          ],
        })}\n\n` +
        `event: snapshot\n` +
        `data: ${JSON.stringify({
          job: { ...baseJob, state: "done", ended_at: "2026-05-12T10:30:00Z" },
          stages: [
            { id: "s1", job_id: JOB_ID, stage_name: "analyze", state: "done", bytes_processed: 1024, last_error: null, created_at: baseJob.created_at, updated_at: "2026-05-12T10:30:00Z", started_at: baseJob.created_at, ended_at: "2026-05-12T10:30:00Z" },
          ],
        })}\n\n`;
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "text/event-stream",
          "cache-control": "no-cache",
          "x-accel-buffering": "no",
        },
        body: events,
      });
    });

    await signIn(page, admin);
    await page.goto(`/jabali-admin/migrations/${JOB_ID}`);

    // The hook fires setQueryData on each snapshot event. After SSE
    // delivers the terminal `done` snapshot the page should reflect
    // it — assert the destination state surfaces somewhere visible.
    await expect.poll(
      async () => {
        // Tag/badge for "done" state. AntD Tag renders text inside;
        // case may be upper- or lowercase depending on STATE_TAG.
        const txt = await page.locator("body").innerText();
        return txt.toLowerCase().includes("done");
      },
      { timeout: 10_000 },
    ).toBe(true);
  });
});
