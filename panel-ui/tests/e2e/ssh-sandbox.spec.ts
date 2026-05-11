// M13: SSH shell sandbox — Server Settings UI + Package sandbox field.
//
// Verifies the "Shell Sandbox" section in Server Settings → General tab:
//   - mode selector renders with bubblewrap/nspawn options
//   - switching to nspawn mode reveals the image version dropdown
//   - nspawn images list is fetched from /admin/system/nspawn-images
//   - saving a nspawn mode PATCH sends the correct body
//
// All API calls are mocked — no real jabali-panel server required.
import { admin, mockApi, signIn, test, expect } from "./fixtures";
import type { Page } from "@playwright/test";

const baseSettings = {
  id: 1,
  hostname: "jabali-panel.local",
  public_ipv4: "10.0.0.1",
  public_ipv6: "",
  ns1_name: "ns1.jabali-panel.local",
  ns1_ipv4: "10.0.0.1",
  ns2_name: "ns2.jabali-panel.local",
  ns2_ipv4: "10.0.0.2",
  admin_email: "admin@jabali-panel.local",
  timezone: "UTC",
  ssh_port: 22,
  ssh_password_auth: false,
  ssh_user_password_auth: false,
  ssh_sandbox_mode: "bubblewrap",
  default_nspawn_image_version: "",
  disk_quota_enabled: false,
  bandwidth_quota_enforce_enabled: false,
  upload_max_size_mb: 100,
  postgres_enabled: false,
  postgres_max_connections_per_user: 5,
  updated_at: "2026-01-01T00:00:00Z",
};

const fakeImages = [
  { name: "debian-13-v1" },
  { name: "debian-13-v2" },
];

async function setupSettingsMocks(page: Page, overrides: Partial<typeof baseSettings> = {}) {
  const settings = { ...baseSettings, ...overrides };

  await page.route("**/admin/settings", async (route) => {
    if (route.request().method() === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(settings),
      });
    }
    if (route.request().method() === "PATCH") {
      const patch = route.request().postDataJSON() as Record<string, unknown>;
      Object.assign(settings, patch);
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(settings),
      });
    }
    return route.continue();
  });

  await page.route("**/admin/system/nspawn-images", async (route) => {
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ images: fakeImages }),
    });
  });

  // Stub other settings-page sub-endpoints so they don't ECONNREFUSED
  await page.route("**/admin/settings/dns*", async (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(settings) }),
  );
  await page.route("**/admin/settings/storage*", async (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(settings) }),
  );
  await page.route("**/admin/panel-cert", async (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ status: "none" }) }),
  );
}

test.describe("M13: SSH shell sandbox — Server Settings", () => {
  test("Shell Sandbox section renders in General tab", async ({ page }) => {
    await mockApi(page, { me: admin });
    await setupSettingsMocks(page);

    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");
    await page.waitForLoadState("networkidle");

    // General tab is the default — Shell Sandbox divider must be visible
    await expect(page.getByText("Shell Sandbox")).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(/bubblewrap.*lightweight|lightweight.*bubblewrap/i)).toBeVisible();

    // Sandbox Mode label + select widget
    await expect(page.getByText("Sandbox Mode")).toBeVisible();
  });

  test("Bubblewrap mode is pre-selected when settings say bubblewrap", async ({ page }) => {
    await mockApi(page, { me: admin });
    await setupSettingsMocks(page, { ssh_sandbox_mode: "bubblewrap" });

    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");
    await page.waitForLoadState("networkidle");

    // The Select for ssh_sandbox_mode should display the bubblewrap option text
    await expect(page.getByText("Shell Sandbox")).toBeVisible({ timeout: 15_000 });
    const modeSelect = page.locator(".ant-select").filter({ hasText: /bubblewrap/i }).first();
    await expect(modeSelect).toBeVisible();
  });

  test("Switching to nspawn mode shows image selector populated from API", async ({ page }) => {
    await mockApi(page, { me: admin });
    await setupSettingsMocks(page, { ssh_sandbox_mode: "bubblewrap" });

    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");
    await page.waitForLoadState("networkidle");

    await expect(page.getByText("Shell Sandbox")).toBeVisible({ timeout: 15_000 });

    // Open the sandbox mode select and pick nspawn
    const modeSelect = page.locator("text=Sandbox Mode").locator("..").locator(".ant-select").first();
    await modeSelect.click();
    await page.getByText(/systemd-nspawn.*full container/i).click();

    // Image version dropdown should now appear
    await expect(page.getByText("Default nspawn Image")).toBeVisible({ timeout: 5_000 });

    // Open the image dropdown and verify fake images appear
    const imageSelect = page.locator("text=Default nspawn Image").locator("..").locator(".ant-select").first();
    await imageSelect.click();
    await expect(page.getByText("debian-13-v1")).toBeVisible({ timeout: 5_000 });
    await expect(page.getByText("debian-13-v2")).toBeVisible();
  });

  test("Saving nspawn mode sends correct PATCH body", async ({ page }) => {
    let patchBody: Record<string, unknown> | null = null;

    await mockApi(page, { me: admin });
    await setupSettingsMocks(page, { ssh_sandbox_mode: "bubblewrap" });

    // Override PATCH to capture body
    await page.route("**/admin/settings", async (route) => {
      if (route.request().method() === "PATCH") {
        patchBody = route.request().postDataJSON() as Record<string, unknown>;
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ...baseSettings, ssh_sandbox_mode: "nspawn", default_nspawn_image_version: "debian-13-v1" }),
        });
      }
      return route.continue();
    });

    await signIn(page, admin);
    await page.goto("/jabali-admin/settings");
    await page.waitForLoadState("networkidle");

    await expect(page.getByText("Shell Sandbox")).toBeVisible({ timeout: 15_000 });

    // Switch mode to nspawn
    const modeSelect = page.locator("text=Sandbox Mode").locator("..").locator(".ant-select").first();
    await modeSelect.click();
    await page.getByText(/systemd-nspawn.*full container/i).click();

    // Pick an image
    await page.waitForSelector("text=Default nspawn Image", { timeout: 5_000 });
    const imageSelect = page.locator("text=Default nspawn Image").locator("..").locator(".ant-select").first();
    await imageSelect.click();
    await page.getByText("debian-13-v1").click();

    // Save
    await page.getByRole("button", { name: /save/i }).first().click();
    await page.waitForResponse("**/admin/settings");

    expect(patchBody).not.toBeNull();
    expect(patchBody!["ssh_sandbox_mode"]).toBe("nspawn");
    expect(patchBody!["default_nspawn_image_version"]).toBe("debian-13-v1");
  });
});
