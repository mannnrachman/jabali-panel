// Dashboard E2E — verifies the admin dashboard renders system info and
// services from mocked agent endpoints.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

test.describe("admin dashboard", () => {
  test("shows system info after sign-in", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);

    // Admin lands on dashboard by default.
    await expect(page).toHaveURL(/\/jabali-admin\/dashboard/);
    await expect(page.getByRole("heading", { name: /dashboard/i })).toBeVisible();

    // System card shows hostname from mock.
    await expect(page.getByText("test-server")).toBeVisible();

    // Memory progress bar exists.
    await expect(page.locator(".ant-progress").first()).toBeVisible();
  });

  test("shows services table", async ({ page }) => {
    await mockApi(page, { me: admin });
    await signIn(page, admin);
    await expect(page).toHaveURL(/\/jabali-admin\/dashboard/);

    // Services table should show the mock services.
    await expect(page.getByText("nginx")).toBeVisible();
    await expect(page.getByText("mariadb")).toBeVisible();
  });
});
