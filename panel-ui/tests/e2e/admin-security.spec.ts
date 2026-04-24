// M26: Admin Security tab E2E.
//
// Three-tab page (CrowdSec / ModSecurity / UFW) at /jabali-admin/security.
// Tests cover the journeys with the lowest blast radius — every UFW
// destructive flow (enable / disable firewall) is SKIPPED by design,
// because a misconfigured run on the CI VM would lock the runner out
// of SSH and fail every subsequent test in the suite.
//
// Required env to run:
//   E2E_BASE_URL          https://jabali-panel.local
//   E2E_USERNAME          admin@example.com
//   E2E_PASSWORD          ...
//   E2E_M26_PROBE_DOMAIN  domain whose vhost we toggle ModSec on (must exist)
//   E2E_M26_PROBE_HOST    publicly resolvable host for that domain (curl target)
//
// Skipped automatically on CI/dev hosts that don't set them.

import { execFileSync } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME ||
  !process.env.E2E_PASSWORD ||
  !process.env.E2E_M26_PROBE_DOMAIN ||
  !process.env.E2E_M26_PROBE_HOST
    ? "E2E_USERNAME, E2E_PASSWORD, E2E_M26_PROBE_DOMAIN, E2E_M26_PROBE_HOST not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `M26 E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M26: Admin Security tab", () => {
    test.setTimeout(180_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";
    const probeDomain = process.env.E2E_M26_PROBE_DOMAIN || "";
    const probeHost = process.env.E2E_M26_PROBE_HOST || "";
    const banIP = "10.0.0.99";

    async function login(page: Page): Promise<void> {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-admin/);
    }

    async function openSecurity(page: Page, tab: "crowdsec" | "modsec" | "ufw"): Promise<void> {
      await page.goto(`/jabali-admin/security?tab=${tab}`);
      await expect(page.getByRole("tab", { name: /CrowdSec/i })).toBeVisible();
      await expect(page.getByRole("tab", { name: /ModSecurity/i })).toBeVisible();
      await expect(page.getByRole("tab", { name: /UFW/i })).toBeVisible();
    }

    test("Security shell renders three tabs", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
    });

    test("CrowdSec: add ban → row visible → delete → gone", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");

      await page.getByRole("button", { name: /Add decision/i }).click();
      const modal = page.getByRole("dialog");
      await modal.getByLabel(/IP or CIDR/i).fill(banIP);
      await modal.getByLabel(/Duration/i).fill("4h");
      await modal.getByLabel(/Reason/i).fill("e2e test ban");
      await modal.getByRole("button", { name: /Add ban/i }).click();

      // Decision row appears
      await expect(page.getByText(banIP, { exact: false })).toBeVisible({ timeout: 15_000 });

      // Delete via Popconfirm
      const row = page.locator("tr", { hasText: banIP }).first();
      await row.getByRole("button", { name: /Delete/i }).click();
      await page.getByRole("button", { name: /Remove/i }).click();
      await expect(page.getByText(banIP, { exact: false })).toHaveCount(0, { timeout: 15_000 });
    });

    test("ModSec: enable global engine + per-domain → XSS probe blocked", async ({ page }) => {
      await login(page);
      await openSecurity(page, "modsec");

      // Flip global engine to On
      await page.getByRole("button", { name: /On \(inspect \+ block\)/i }).click();
      await page.getByRole("button", { name: /^Apply$/ }).click();
      await page.getByRole("button", { name: /^Apply$/ }).click(); // popconfirm
      await expect(page.getByText(/global config updated/i)).toBeVisible({ timeout: 30_000 });

      // Toggle the probe domain on
      const domainRow = page.locator("tr", { hasText: probeDomain }).first();
      const sw = domainRow.locator(".ant-switch").first();
      const checked = await sw.getAttribute("aria-checked");
      if (checked !== "true") {
        await sw.click();
        await expect(page.getByText(/Enabled ModSec/i)).toBeVisible({ timeout: 15_000 });
      }

      // Wait for nginx reload to propagate (5-10s on a warm host).
      await page.waitForTimeout(10_000);

      // XSS probe — CRS rule 941100 should block this on paranoia >= 1
      const probeURL = `https://${probeHost}/?q=%3Cscript%3Ealert(1)%3C/script%3E`;
      const status = curlStatus(probeURL);
      expect(status).toBe(403);

      // Cleanup: leave engine state alone (operator decides) but disable
      // the per-domain switch we toggled if we toggled it.
      if (checked !== "true") {
        await sw.click();
      }
    });

    test("UFW: add rule → row visible → delete → gone", async ({ page }) => {
      await login(page);
      await openSecurity(page, "ufw");

      // Adding a rule on an inactive firewall is harmless (UFW stores
      // it for next enable). The destructive enable/disable flows are
      // out of scope to keep CI from locking itself out.
      const port = "61999";
      await page.getByLabel(/Port/i).fill(port);
      await page.getByRole("button", { name: /Add rule/i }).click();
      await expect(page.getByText(/Rule added/i)).toBeVisible({ timeout: 15_000 });
      await expect(page.locator("tr", { hasText: port })).toBeVisible({ timeout: 15_000 });

      const row = page.locator("tr", { hasText: port }).first();
      await row.getByRole("button", { name: /Delete/i }).click();
      await page.getByRole("button", { name: /^Delete$/ }).click(); // popconfirm
      await expect(page.locator("tr", { hasText: port })).toHaveCount(0, { timeout: 15_000 });
    });

    test.skip("UFW: enable / disable firewall flow", async () => {
      // Out of scope — flipping the firewall on a CI VM that talks to
      // the runner over SSH risks locking the runner out for the rest
      // of the suite. Operators verify this manually with the runbook.
    });

    // ---- M27 smoke tests -------------------------------------------------

    test("M27 Allowlist: add IP → row visible → remove → gone", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/security?tab=crowdsec");
      await page.waitForLoadState("networkidle");
      await expect(page.getByText(/Allowlist \(never ban\)/i)).toBeVisible();

      const ip = `198.51.100.${Math.floor(Math.random() * 200) + 10}`;
      await page.getByRole("button", { name: /Add to allowlist/i }).click();
      await page.getByLabel("IP or CIDR").fill(ip);
      await page.getByLabel("Reason").fill("e2e-smoke");
      await page.getByRole("button", { name: /^Add$/ }).click();

      await expect(page.locator("tr", { hasText: ip })).toBeVisible({ timeout: 15_000 });

      const row = page.locator("tr", { hasText: ip }).first();
      await row.getByRole("button", { name: /Remove/i }).click();
      await page.getByRole("button", { name: /^Remove$/ }).click(); // popconfirm
      await expect(page.locator("tr", { hasText: ip })).toHaveCount(0, { timeout: 15_000 });
    });

    test("M27 Alerts view: table renders", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/security?tab=crowdsec");
      await page.waitForLoadState("networkidle");
      await expect(page.getByText(/Alerts \(last 24h\)/i)).toBeVisible();
      const emptyOrRow = page
        .getByText(/No alerts in the last 24h/i)
        .or(page.locator("tr.ant-table-row").first());
      await expect(emptyOrRow).toBeVisible();
    });

    test("M27 Console card: validation blocks short keys", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/security?tab=crowdsec");
      await page.waitForLoadState("networkidle");
      await expect(page.getByText(/CrowdSec Console \(optional\)/i)).toBeVisible();
      await page.getByLabel(/Enrollment key/i).fill("too-short");
      await page.getByRole("button", { name: /^Enroll$/ }).click();
      await expect(page.getByText(/16-128 alnum \+ dash chars/i)).toBeVisible();
    });

    test("M27 Captcha: enabling reveals key inputs", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/security?tab=crowdsec");
      await page.waitForLoadState("networkidle");
      await expect(page.getByText(/^Captcha remediation$/i)).toBeVisible();
      const onButton = page.getByRole("radio", { name: "On" });
      if (await onButton.isVisible()) {
        await onButton.click();
        await expect(page.getByLabel("Site key (public)")).toBeEnabled();
        await page.getByRole("radio", { name: "Off" }).click();
      }
    });

    test("M27 Profiles: scenarios table renders", async ({ page }) => {
      await login(page);
      await page.goto("/jabali-admin/security?tab=crowdsec");
      await page.waitForLoadState("networkidle");
      await expect(page.getByText(/Per-scenario remediation override/i)).toBeVisible();
      const emptyOrRow = page
        .getByText(/No scenarios installed/i)
        .or(page.locator("tr.ant-table-row").first());
      await expect(emptyOrRow).toBeVisible({ timeout: 15_000 });
    });
  });
}

// curlStatus issues a non-following GET and returns the HTTP status,
// or 0 on connection failure. -k skips TLS verification (test certs).
function curlStatus(url: string): number {
  try {
    const out = execFileSync(
      "curl",
      ["-k", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10", url],
      { encoding: "utf8" },
    );
    return parseInt(out.trim(), 10) || 0;
  } catch {
    return 0;
  }
}
