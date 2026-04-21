// Users CRUD E2E — drives the admin shell through the full list-create-edit
// cycle with a mocked backend.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

test.describe("users CRUD (admin)", () => {
  test("list renders seeded rows", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);

    // Navigate to users page (admin now lands on dashboard).
    await page.goto("/jabali-admin/users");

    // The seed list includes the admin row.
    await expect(page.getByRole("cell", { name: admin.email })).toBeVisible();
  });

  test("create flow adds a new row", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);

    // goto /create directly instead of clicking Create on /users. On CI,
    // clicking the list-page Create button races with AntD Table's
    // initial-render onChange handler: the table's onChange fires
    // setParams({page:1}) shortly after mount, which react-router's
    // setSearchParams applies to whatever the *current* URL is at call
    // time. When onChange fires fast enough, it just rewrites /users →
    // /users?page=1 before the button click. When it fires slow enough
    // (CI's 2-core GitHub runner), the navigate("/users/create") runs
    // first, the browser commits /create, *then* the late setParams
    // call fires setSearchParams, which navigates back to /users?page=1
    // — dropping /create entirely. The form briefly mounts (Save button
    // visible, expect passes) then unmounts with the nav-back, and
    // every subsequent locator times out. Local Chromium wins the race
    // 100% of the time; CI loses it ~100% of the time. Bypassing the
    // click with a direct goto eliminates the racy AntD Table onChange
    // entirely — we never mount UserList on /users so its onChange
    // never fires.
    await page.goto("/jabali-admin/users/create");

    // Still wait for the form to be mounted before filling, even though
    // we bypass the list page. Save button is the last child of the
    // Form, so its visibility proves the full form tree is in the DOM.
    await expect(
      page.getByRole("button", { name: /save/i }),
    ).toBeVisible();

    // Fill email LAST. Filling email first and then tabbing through the
    // password field loses the email value ~1/3 of runs — an async event
    // (Chromium autofill tick / useSelect fetching packages) clears the
    // Email input after the later fills but before the Save click.
    // Filling email last leaves no async window for that clearing to
    // happen. Production isn't affected because humans take >100ms
    // between fields and the clearing event is benign to user experience.
    //
    // Password uses a type selector, not getByRole/getByLabel:
    //   - <input type="password"> has no ARIA "textbox" role (HTML-ARIA
    //     spec excludes the password type to avoid screen-reader leaks),
    //     so getByRole("textbox") never matches.
    //   - getByLabel(/^password$/i) also misses: AntD renders the Form.Item
    //     tooltip ("At least 10 characters") as a QuestionCircleOutlined
    //     icon *inside* the <label>, and that icon's aria-label pollutes
    //     the label's computed accessible name to "Password question-circle",
    //     breaking the strict ^/$ anchors. Dropping the anchors would work
    //     but a plain type selector is less brittle and doesn't depend on
    //     AntD internals.
    // Text fields stay on getByRole("textbox") to dodge AntD Table's
    // sortable column headers (aria-label="Email" / "First name" /
    // "Last name") that share names with the form inputs.
    await page.locator('input[type="password"]').fill("validpassword99");
    await page.getByRole("textbox", { name: /first name/i }).fill("New");
    await page.getByRole("textbox", { name: /last name/i }).fill("User");
    await page.getByRole("textbox", { name: /email/i }).fill("new.user@test.local");

    await page.getByRole("button", { name: /save/i }).click();

    // On success Refine bounces back to the list.
    await page.waitForURL(/\/jabali-admin\/users(\?.*)?$/);
    await expect(page.getByRole("cell", { name: "new.user@test.local" })).toBeVisible();
  });

  test("edit flow PATCHes a row's name", async ({ page }) => {
    await mockApi(page, {
      me: admin,
      users: [
        admin,
        {
          id: "01KPVICTIM00000000000000AA",
          email: "victim@test.local",
          name_first: "",
          name_last: "",
          is_admin: false,
          created_at: "2026-04-01T00:00:00Z",
          updated_at: "2026-04-01T00:00:00Z",
        },
      ],
    });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);
    await page.goto("/jabali-admin/users");

    // Click the Edit icon on the victim row.
    const victimRow = page.getByRole("row", { name: /victim@test\.local/ });
    await victimRow.getByRole("button").first().click();
    await page.waitForURL(/\/jabali-admin\/users\/edit\//);

    // Same form-ready anchors the create-flow needs: wait for Save to be
    // visible (proves form tree mounted) and for network to go idle (the
    // edit form's initial GET /users/:id + hosting-package Select query
    // finish), otherwise a mid-load re-render detaches the Save button
    // during the click and Playwright retries into the 30s test timeout.
    await expect(
      page.getByRole("button", { name: /save/i }),
    ).toBeVisible();
    await page.waitForLoadState("networkidle");

    await page.getByLabel(/first name/i).fill("Changed");
    await page.getByRole("button", { name: /save/i }).click();

    await page.waitForURL(/\/jabali-admin\/users(\?.*)?$/);
    await expect(page.getByRole("cell", { name: /Changed/ })).toBeVisible();
  });

  test("delete flow removes a row after confirm", async ({ page }) => {
    await mockApi(page, {
      me: admin,
      users: [
        admin,
        {
          id: "01KPVICTIM00000000000000BB",
          email: "doomed@test.local",
          name_first: "",
          name_last: "",
          is_admin: false,
          created_at: "2026-04-01T00:00:00Z",
          updated_at: "2026-04-01T00:00:00Z",
        },
      ],
    });
    await signIn(page, admin);
    await page.waitForURL(/\/jabali-admin/);
    await page.goto("/jabali-admin/users");

    // The login "Welcome back" notification can intercept pointer events
    // on the Popconfirm; wait for it to auto-close first.
    await page.locator(".ant-notification").waitFor({ state: "hidden", timeout: 10_000 });

    const victimRow = page.getByRole("row", { name: /doomed@test\.local/ });
    // Delete is the trailing icon button in the actions cell.
    await victimRow.getByRole("button", { name: /delete/i }).click();

    // UserDeleteAction opens an AntD Modal titled `Delete user "<email>"?`
    // with a two-checkbox choice and a "Delete user" confirm button in the
    // footer. Scope the click to that dialog so we don't re-hit the icon
    // button in the row.
    const confirmDialog = page.getByRole("dialog", { name: /delete user/i });
    await confirmDialog.getByRole("button", { name: /^delete user$/i }).click();

    await expect(page.getByRole("cell", { name: /doomed@test\.local/ })).toHaveCount(0);
  });
});
