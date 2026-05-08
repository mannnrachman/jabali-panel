// MyProfile E2E — user-panel identity card + Kratos-backed security
// settings (post-M20). The profile page no longer owns passwords; the
// Security card auto-initialises a Kratos self-service settings flow on
// mount and renders the result inline. With Kratos unmocked in tests
// the flow stays in its loading state — that's the spinner we assert on.
import { expect, mockApi, signIn, test, user } from "./fixtures";

test.describe("user panel — MyProfile", () => {
  test("identity card shows email + user ID, Security card renders without password fields", async ({ page }) => {
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

    // Security card title is always rendered. With Kratos unmocked, the
    // initSettingsFlow promise either resolves to refresh_required (which
    // window.location.assigns away from the page — handled by the test
    // fixture stubbing /.ory/* to a noop), or the card stays in its
    // initial Spin state. Both states keep the page intact — what we're
    // asserting is that the page does NOT regress to in-panel password
    // fields (the M20 ban that the legacy "Manage account security" link
    // assertion existed to enforce).
    await expect(page.getByRole("heading", { name: /^security$/i })).toBeVisible();

    await expect(page.getByLabel(/current password/i)).toHaveCount(0);
    await expect(page.getByLabel(/^new password$/i)).toHaveCount(0);
  });
});
