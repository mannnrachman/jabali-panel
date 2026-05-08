// Adminer SSO E2E — drives the user databases shell through the
// "Open in Adminer" click and asserts:
//   1. POST /api/v1/sso/adminer fires with the right database_id.
//   2. The mint endpoint's redirect_url opens in a new tab.
//   3. The new tab URL preserves the token + db + engine query
//      parameters so the Adminer plugin's auto-submit form has the
//      data it needs.
//
// We don't drive the actual Adminer PHP page (that's a server-side
// integration test on the live VM, not a Playwright run with a mocked
// API). The asserts above cover the panel-side contract — anything
// past the redirect is the plugin's responsibility.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

test.describe("adminer SSO (M37 Phase 4)", () => {
  test("user clicks Open in Adminer → new tab on /jabali-adminer/?token=...", async ({ page, context }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-(admin|panel)/);

    // Stub the user databases list with one MariaDB row + one PG row
    // so the action column renders both engine paths.
    await page.route("**/api/v1/databases**", async (route) => {
      const url = route.request().url();
      if (route.request().method() !== "GET") return route.continue();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: [
            {
              id: "01J0DBMARIA0000000000000000",
              user_id: admin.id,
              name: "shukivaknin_wp",
              engine: "mariadb",
              charset: "utf8mb4",
              size_bytes: 1024,
              created_at: "2026-05-01T10:00:00Z",
              updated_at: "2026-05-01T10:00:00Z",
            },
            {
              id: "01J0DBPGSQL0000000000000000",
              user_id: admin.id,
              name: "shukivaknin_app",
              engine: "postgres",
              charset: "UTF8",
              size_bytes: 0,
              created_at: "2026-05-01T11:00:00Z",
              updated_at: "2026-05-01T11:00:00Z",
            },
          ],
          total: 2,
          page: 1,
          page_size: 25,
        }),
      });
      void url;
    });

    // Stub the SSO mint endpoint. Capture the request body so we can
    // assert which database_id the click sent.
    let mintBody: { database_id?: string } | null = null;
    await page.route("**/api/v1/sso/adminer", async (route) => {
      mintBody = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          redirect_url:
            "https://mx.jabali-panel.local:8443/jabali-adminer/?token=t0kenABC&db=shukivaknin_app&engine=postgres",
        }),
      });
    });

    await page.goto("/jabali-panel/databases");
    await expect(page.getByRole("cell", { name: "shukivaknin_app" })).toBeVisible();

    // Click "Open in Adminer" on the postgres row, then capture the
    // new tab. window.open is fired synchronously so Playwright sees
    // it via context.waitForEvent('page').
    const newTabPromise = context.waitForEvent("page");
    const pgRow = page.getByRole("row", { name: /shukivaknin_app/ });
    await pgRow.getByRole("button", { name: /Open in Adminer/i }).click();
    const newTab = await newTabPromise;

    // The blank tab is opened first then navigated; wait for the URL
    // to stabilise on the Adminer redirect.
    await newTab.waitForURL(/\/jabali-adminer\/\?/);
    const url = newTab.url();
    expect(url).toContain("token=t0kenABC");
    expect(url).toContain("db=shukivaknin_app");
    expect(url).toContain("engine=postgres");

    expect(mintBody).not.toBeNull();
    expect(mintBody!.database_id).toBe("01J0DBPGSQL0000000000000000");

    await newTab.close();
  });
});
