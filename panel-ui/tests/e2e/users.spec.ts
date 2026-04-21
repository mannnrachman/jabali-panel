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
    await page.goto("/jabali-admin/users");

    await page.getByRole("button", { name: /create/i }).first().click();
    await page.waitForURL(/\/jabali-admin\/users\/create/);

    // Wait for the form to finish its initial render cycle before
    // touching inputs. waitForURL only confirms the URL changed —
    // the target page may still have async effects in flight (the
    // Hosting Package <Select> hydrates via useSelectQuery, the
    // AuthContext whoami observer may be rebroadcasting a fresh
    // identity). On fast local Chromium these settle in microseconds.
    // On the host-mode CI runner's slower, CPU-contended Chromium
    // they can still be churning when .fill() starts, and a nearby
    // re-render detaches the input mid-fill — Playwright reports
    // "element was detached from the DOM, retrying" and loops until
    // the 30s test timeout. Waiting for the submit button (the last
    // child of the Form) to be visible proves the full form tree is
    // mounted, and waiting for networkidle gives the initial packages
    // fetch a chance to complete.
    await expect(
      page.getByRole("button", { name: /save/i }),
    ).toBeVisible();
    await page.waitForLoadState("networkidle");

    // Fill email LAST. Filling email first and then tabbing through the
    // password field loses the email value ~1/3 of runs — an async event
    // (Chromium autofill tick / useSelect fetching packages) clears the
    // Email input after the later fills but before the Save click.
    // Filling email last leaves no async window for that clearing to
    // happen. Production isn't affected because humans take >100ms
    // between fields and the clearing event is benign to user experience.
    //
    // Password uses getByLabel, not getByRole("textbox"): AntD's
    // <Input.Password> renders <input type="password">, which has NO
    // ARIA "textbox" role (the password type is excluded from the role
    // by the HTML-ARIA spec to avoid screen-reader leaks). Text fields
    // stay on getByRole("textbox") to skip AntD Table's sortable column
    // headers (aria-label="Email" / "First name" / "Last name") that
    // would otherwise race the form inputs on a getByLabel match.
    await page.getByLabel(/^password$/i).fill("validpassword99");
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
