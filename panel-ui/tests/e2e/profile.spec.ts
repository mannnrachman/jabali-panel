// MyProfile E2E — user-panel identity card + redirect-to-Kratos security flow.
// Post-M20 the panel no longer owns passwords or 2FA; the profile page's
// Security card links out to Kratos's self-service settings flow.
import { expect, mockApi, signIn, test, user } from "./fixtures";

test.describe("user panel — MyProfile", () => {
  test("identity card shows email + user ID, Security card links to Kratos", async ({ page }) => {
    await mockApi(page, { me: user });
    await signIn(page, user);
    await page.waitForURL(/\/jabali-panel/);

    // da73d78: user shell lands on /dashboard now, not /profile. The
    // profile page still exists (reachable from header dropdown) and
    // this spec covers its content, so navigate explicitly.
    await page.goto("/jabali-panel/profile");
    await expect(page.getByRole("heading", { name: /my profile/i })).toBeVisible();

    // Email appears in both the header avatar button and the Descriptions
    // card — scope to the Descriptions to avoid Playwright strict-mode.
    await expect(
      page.locator(".ant-descriptions").getByText(user.email),
    ).toBeVisible();

    // Security card: the "Manage account security" button must point at
    // Kratos's self-service settings browser flow. No password fields on
    // this page anymore.
    const settingsLink = page.getByRole("link", {
      name: /manage account security/i,
    });
    await expect(settingsLink).toBeVisible();
    await expect(settingsLink).toHaveAttribute(
      "href",
      "/.ory/self-service/settings/browser",
    );

    await expect(page.getByLabel(/current password/i)).toHaveCount(0);
    await expect(page.getByLabel(/^new password$/i)).toHaveCount(0);
  });
});
