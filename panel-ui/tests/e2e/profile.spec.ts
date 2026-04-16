// MyProfile E2E — user-panel change-password flow.
import { expect, mockApi, signIn, test, user } from "./fixtures";

test.describe("user panel — MyProfile", () => {
  test("change password submits PATCH with current_password + password", async ({ page }) => {
    await mockApi(page, { me: user });
    await signIn(page, user);
    await page.waitForURL(/\/jabali-panel/);

    await expect(page.getByRole("heading", { name: /my profile/i })).toBeVisible();
    // Email appears in both the header avatar button and the Descriptions
    // card — scope to the Descriptions to avoid Playwright strict-mode.
    await expect(
      page.locator(".ant-descriptions").getByText(user.email),
    ).toBeVisible();

    await page.getByLabel(/current password/i).fill("oldpassword99");
    await page.getByLabel(/^new password$/i).fill("newpassword99");

    // Intercept the PATCH to confirm shape — mockApi already accepts it,
    // but we want to assert the payload really contains both fields.
    const [request] = await Promise.all([
      page.waitForRequest(
        (req) => req.url().includes(`/api/v1/users/${user.id}`) && req.method() === "PATCH",
      ),
      page.getByRole("button", { name: /update password/i }).click(),
    ]);

    const body = request.postDataJSON() as {
      current_password?: string;
      password?: string;
    };
    expect(body.current_password).toBe("oldpassword99");
    expect(body.password).toBe("newpassword99");

    await expect(page.getByText(/password updated/i)).toBeVisible();
  });
});
