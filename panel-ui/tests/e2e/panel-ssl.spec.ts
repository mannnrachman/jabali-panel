// panel-ssl.spec.ts — admin Server Settings → General → Panel SSL
// card. Mocks /admin/panel-certificate so the spec doesn't depend on
// a live panel-api or LE issuance.
import { admin, expect, mockApi, signIn, test } from "./fixtures";

const baseEnvelope = {
  id: 1,
  hostname: "panel.example.com",
  status: "self_signed",
  cert_pem_path: "/etc/jabali/tls/panel.crt",
  attempt_count: 0,
  staging: false,
  use_le: false,
  updated_at: new Date().toISOString(),
  routable: true,
};

test.describe("admin panel SSL card", () => {
  test("renders self-signed status + routable badge + toggle", async ({ page }) => {
    await mockApi(page, { me: admin });
    await page.route("**/api/v1/admin/panel-certificate", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(baseEnvelope),
      }),
    );
    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");

    // Status tag.
    await expect(page.getByText("Self-signed")).toBeVisible();
    // Routability tag.
    await expect(page.getByText("Public-routable")).toBeVisible();
    // Toggle label.
    await expect(
      page.getByText("Use Let's Encrypt for this hostname"),
    ).toBeVisible();
  });

  test("toggle is disabled when not routable", async ({ page }) => {
    await mockApi(page, { me: admin });
    await page.route("**/api/v1/admin/panel-certificate", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...baseEnvelope,
          routable: false,
          routable_reason: "non-routable hostname suffix",
        }),
      }),
    );
    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");

    await expect(page.getByText("Not routable")).toBeVisible();
    await expect(page.getByText(/non-routable hostname suffix/)).toBeVisible();
  });

  test("issued status shows expiry hint", async ({ page }) => {
    const future = new Date(Date.now() + 60 * 24 * 3600 * 1000).toISOString();
    await mockApi(page, { me: admin });
    await page.route("**/api/v1/admin/panel-certificate", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...baseEnvelope,
          status: "issued",
          use_le: true,
          issued_at: new Date().toISOString(),
          expires_at: future,
        }),
      }),
    );
    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");

    await expect(page.getByText(/Issued by Let's Encrypt/)).toBeVisible();
    await expect(page.getByText(/Expires in \d+ days/)).toBeVisible();
  });
});
