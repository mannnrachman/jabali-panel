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

    await page.getByLabel(/email/i).fill("new.user@test.local");
    await page.getByLabel(/^password/i).fill("validpassword99");
    await page.getByLabel(/first name/i).fill("New");
    await page.getByLabel(/last name/i).fill("User");

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

    // Refine's Popconfirm: target the confirm button inside the popover
    // overlay, not the table's icon buttons.
    await page.locator(".ant-popconfirm-buttons").getByRole("button", { name: /delete/i }).click();

    await expect(page.getByRole("cell", { name: /doomed@test\.local/ })).toHaveCount(0);
  });
});
