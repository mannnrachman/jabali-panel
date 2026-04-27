// Security tab — read-only smoke test.
//
// Walks the three top-level tabs (CrowdSec / Malware / Firewall (UFW))
// and the CrowdSec sub-tabs introduced through M26 / M27 / M33,
// asserting each renders its hero element. NO destructive flows: no
// bans added, no firewall toggles, no signature pulls — those have
// dedicated specs (admin-security.spec.ts, admin-malware.spec.ts) and
// run on a manually-vetted host.
//
// Required env:
//   E2E_BASE_URL  https://jabali-panel.local
//   E2E_USERNAME  admin@example.com
//   E2E_PASSWORD  ...
//
// Skipped when env not provided so it doesn't break dev / CI without
// a wired test host.

import { expect, test, type Page } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME / E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `Security smoke skipped: ${SKIP_REASON}`);
} else {
  test.describe("Security smoke (read-only)", () => {
    test.setTimeout(90_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";

    async function login(page: Page) {
      await page.goto("/auth/login");
      await page.fill('input[name="email"], input[type="email"]', username);
      await page.fill('input[name="password"], input[type="password"]', password);
      await page.click('button[type="submit"]');
      await page.waitForURL(/\/jabali-(admin|panel)/, { timeout: 30_000 });
    }

    async function openSecurity(page: Page, tab: "crowdsec" | "malware" | "ufw") {
      await page.goto(`/jabali-admin/security?tab=${tab}`);
      await page.waitForLoadState("networkidle");
    }

    // ---- top-level tab strip ------------------------------------------------

    test("three top-level tabs present", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await expect(page.getByRole("tab", { name: /CrowdSec/i })).toBeVisible();
      await expect(page.getByRole("tab", { name: /Malware/i })).toBeVisible();
      await expect(page.getByRole("tab", { name: /Firewall.*UFW/i })).toBeVisible();
    });

    // ---- CrowdSec sub-tabs --------------------------------------------------

    test("CrowdSec Overview renders About + 5 metric tiles", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");

      // M26 explainer Alert.
      await expect(page.getByText(/What is CrowdSec\?/i)).toBeVisible();

      // Status descriptions.
      await expect(page.getByText(/CrowdSec status/i)).toBeVisible();

      // Metric tiles — labels are text, values are numbers; the labels
      // alone are stable.
      for (const label of [
        "Parsed events",
        "Unparsed",
        "Buckets fired",
        "Active decisions",
        "Total alerts",
      ]) {
        await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
      }
    });

    test("CrowdSec Hub sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /^Hub$/ }).click();
      // Either the recommended-collections card or empty state.
      const heroOrEmpty = page
        .getByText(/Recommended|Installed collections|No items/i)
        .first();
      await expect(heroOrEmpty).toBeVisible({ timeout: 15_000 });
    });

    test("CrowdSec Active decisions sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Active decisions/i }).click();
      await expect(
        page.getByRole("button", { name: /Add decision/i }),
      ).toBeVisible();
    });

    test("CrowdSec Allowlist sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Allowlist/i }).click();
      await expect(page.getByText(/Allowlist \(never ban\)/i)).toBeVisible();
    });

    test("CrowdSec Alerts sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Alerts/i }).click();
      await expect(page.getByText(/Alerts \(last 24h\)/i)).toBeVisible();
    });

    test("CrowdSec Console sub-tab renders + validates short key", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Console/i }).click();
      await expect(page.getByText(/CrowdSec Console/i)).toBeVisible();
      // Reject a too-short enrollment key (validation only — no API call).
      await page.getByLabel(/Enrollment key/i).fill("too-short");
      await page.getByRole("button", { name: /^Enroll$/ }).click();
      await expect(page.getByText(/16-128 alnum/i)).toBeVisible();
    });

    test("CrowdSec Captcha sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /^Captcha$/ }).click();
      await expect(page.getByText(/Captcha remediation/i)).toBeVisible();
    });

    test("CrowdSec Per-scenario sub-tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Per-scenario/i }).click();
      await expect(page.getByText(/Per-scenario remediation override/i)).toBeVisible();
    });

    test("CrowdSec Block Country tab + country picker has options", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Block Country/i }).click();

      // Mode radios visible.
      await expect(page.getByRole("radio", { name: /^Off$/ })).toBeVisible();
      await expect(page.getByRole("radio", { name: /Allow-list/i })).toBeVisible();
      await expect(page.getByRole("radio", { name: /Deny-list/i })).toBeVisible();

      // Switch to Allow-list to reveal the picker, then open dropdown.
      await page.getByRole("radio", { name: /Allow-list/i }).click();
      const picker = page.locator(".ant-select-selector").last();
      await picker.click();
      await page.keyboard.type("israel");
      // Israel should appear (any case) with its alpha-2 code.
      await expect(page.getByText(/Israel \(IL\)/i).first()).toBeVisible({
        timeout: 10_000,
      });
      await page.keyboard.press("Escape");
      await page.getByRole("radio", { name: /^Off$/ }).click();
    });

    test("CrowdSec Bouncers sub-tab renders + AppSec-only tag visible", async ({ page }) => {
      await login(page);
      await openSecurity(page, "crowdsec");
      await page.getByRole("tab", { name: /Bouncers/i }).click();

      // Header columns.
      for (const col of ["Name", "Type", "Last pull", "Status"]) {
        await expect(page.getByRole("columnheader", { name: col })).toBeVisible();
      }
      // The nginx Lua bouncer has API_URL="" by design (ADR-0060), so
      // its Last pull cell renders an "AppSec-only" tag — fixture
      // present on every Jabali install.
      await expect(page.getByText(/AppSec-only/i).first()).toBeVisible({
        timeout: 10_000,
      });
    });

    // ---- Malware tab --------------------------------------------------------

    test("Malware Overview renders status tiles", async ({ page }) => {
      await login(page);
      await openSecurity(page, "malware");
      await expect(page.getByText(/Stack health/i)).toBeVisible();
      for (const label of [
        "ClamAV daemon",
        "Freshclam",
        "Realtime monitor",
        "Tetragon",
      ]) {
        await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
      }
    });

    test("Malware sub-tabs are reachable", async ({ page }) => {
      await login(page);
      await openSecurity(page, "malware");
      for (const tab of [
        "Overview",
        "Quarantine",
        "Events",
        "Manual scan",
        "YARA rules",
        "Tetragon",
        "Settings",
      ]) {
        await expect(page.getByRole("tab", { name: new RegExp(`^${tab}$`, "i") })).toBeVisible();
      }
    });

    test("Manual scan defaults to Path mode + Users mode loads picker", async ({ page }) => {
      await login(page);
      await openSecurity(page, "malware");
      await page.getByRole("tab", { name: /Manual scan/i }).click();
      await expect(page.getByRole("radio", { name: /^Path$/ })).toBeVisible();
      await expect(page.getByRole("radio", { name: /^Users$/ })).toBeVisible();
      await page.getByRole("radio", { name: /^Users$/ }).click();
      await expect(
        page.getByRole("button", { name: /Scan selected/i }),
      ).toBeVisible();
    });

    // ---- UFW tab ------------------------------------------------------------

    test("Firewall (UFW) tab renders", async ({ page }) => {
      await login(page);
      await openSecurity(page, "ufw");
      // Either the rules table or the firewall-disabled banner is visible.
      const rulesOrDisabled = page
        .getByText(/Firewall is (enabled|disabled)|Add rule|UFW/i)
        .first();
      await expect(rulesOrDisabled).toBeVisible({ timeout: 15_000 });
    });
  });
}
