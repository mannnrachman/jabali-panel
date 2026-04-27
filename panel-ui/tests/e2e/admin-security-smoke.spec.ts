// Security tab — read-only smoke test.
//
// One single login, then walk every Security panel + sub-tab and
// assert each renders its hero element. NO destructive flows
// (no bans added, no firewall toggles, no signature pulls) — those
// have dedicated specs.
//
// Single-test design (vs one test per tab) on purpose:
//   - Kratos rate-limits login at 5/min; one test per tab tripped it.
//   - Smoke = end-to-end walkthrough. Failing fast on the first broken
//     panel is exactly what we want — later panels still need a
//     working shell to even render.
//
// Required env:
//   E2E_BASE_URL  https://jabali-panel.local
//   E2E_USERNAME  admin@example.com
//   E2E_PASSWORD  ...

import { expect, test, type Page } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD
    ? "E2E_USERNAME / E2E_PASSWORD not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `Security smoke skipped: ${SKIP_REASON}`);
} else {
  test.describe.configure({ mode: "serial" });

  test.describe("Security smoke (read-only)", () => {
    test.setTimeout(180_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";

    async function login(page: Page): Promise<void> {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-(admin|panel)/, { timeout: 30_000 });
    }

    async function gotoSecurity(
      page: Page,
      tab: "crowdsec" | "malware" | "ufw",
      sub?: string,
    ): Promise<void> {
      const url = sub
        ? `/jabali-admin/security?tab=${tab}&sub=${sub}`
        : `/jabali-admin/security?tab=${tab}`;
      await page.goto(url);
      await page.waitForLoadState("networkidle");
    }

    test("walk every Security panel + assert hero element", async ({ page }) => {
      await login(page);

      // ---- top-level tabs --------------------------------------------------
      await gotoSecurity(page, "crowdsec");
      await expect(page.getByRole("tab", { name: /CrowdSec/i }).first()).toBeVisible();
      await expect(page.getByRole("tab", { name: /Malware/i }).first()).toBeVisible();
      await expect(page.getByRole("tab", { name: /Firewall.*UFW/i }).first()).toBeVisible();

      // ---- CrowdSec → Overview --------------------------------------------
      await gotoSecurity(page, "crowdsec", "overview");
      await expect(page.getByText(/What is CrowdSec\?/i)).toBeVisible();
      await expect(page.getByText(/CrowdSec status/i)).toBeVisible();
      for (const label of [
        "Parsed events",
        "Unparsed",
        "Buckets fired",
        "Active decisions",
        "Total alerts",
      ]) {
        await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
      }

      // ---- CrowdSec → Hub --------------------------------------------------
      await gotoSecurity(page, "crowdsec", "hub");
      await expect(
        page.getByText(/Recommended|Installed collections|No items|Hub/i).first(),
      ).toBeVisible({ timeout: 15_000 });

      // ---- CrowdSec → Active decisions ------------------------------------
      await gotoSecurity(page, "crowdsec", "decisions");
      await expect(page.getByRole("button", { name: /Add decision/i })).toBeVisible();

      // ---- CrowdSec → Allowlist -------------------------------------------
      await gotoSecurity(page, "crowdsec", "allowlist");
      await expect(page.getByText(/Allowlist \(never ban\)/i)).toBeVisible();

      // ---- CrowdSec → Alerts ----------------------------------------------
      await gotoSecurity(page, "crowdsec", "alerts");
      await expect(page.getByText(/Alerts \(last 24h\)/i)).toBeVisible();

      // ---- CrowdSec → Console ---------------------------------------------
      await gotoSecurity(page, "crowdsec", "console");
      await expect(page.getByText(/CrowdSec Console/i).first()).toBeVisible();
      // Validate enrollment-key form ONLY when the engine isn't already
      // enrolled — on an enrolled host the field is hidden.
      const enrollKey = page.getByLabel(/Enrollment key/i);
      if (await enrollKey.isVisible().catch(() => false)) {
        await enrollKey.fill("too-short");
        await page.getByRole("button", { name: /^Enroll$/ }).click();
        await expect(page.getByText(/16-128 alnum/i)).toBeVisible();
      }

      // ---- CrowdSec → Captcha ---------------------------------------------
      await gotoSecurity(page, "crowdsec", "captcha");
      await expect(page.getByText(/Captcha remediation/i)).toBeVisible();

      // ---- CrowdSec → Per-scenario ----------------------------------------
      await gotoSecurity(page, "crowdsec", "profiles");
      await expect(page.getByText(/Per-scenario remediation override/i)).toBeVisible();

      // ---- CrowdSec → Block Country (with picker) -------------------------
      await gotoSecurity(page, "crowdsec", "appsec");
      // Mode buttons (AntD Radio.Button — visible label, hidden input).
      await expect(page.getByText(/^Mode:/i).first()).toBeVisible();
      await expect(
        page.locator(".ant-radio-button-wrapper").filter({ hasText: /^Off$/ }),
      ).toBeVisible();
      // Switch to Allow-list to reveal the picker.
      await page
        .locator(".ant-radio-button-wrapper")
        .filter({ hasText: /Allow-list/ })
        .click();
      // Picker placeholder visible = ISO-3166 picker mounted. We don't
      // open + search because the page-header Search combobox sits on
      // top in the z-stack and intercepts pointer events on this view.
      await expect(
        page.getByText(/Type a country name or code/i),
      ).toBeVisible();
      // Reset back to Off so we don't leave dirty state in the form.
      await page
        .locator(".ant-radio-button-wrapper")
        .filter({ hasText: /^Off$/ })
        .click();

      // ---- CrowdSec → Bouncers --------------------------------------------
      await gotoSecurity(page, "crowdsec", "bouncers");
      for (const col of ["Name", "Type", "Last pull", "Status"]) {
        await expect(page.getByRole("columnheader", { name: col })).toBeVisible();
      }
      // 'AppSec-only' tag fixture: nginx Lua bouncer never polls LAPI
      // (ADR-0060), so its Last pull cell renders this tag.
      await expect(page.getByText(/AppSec-only/i).first()).toBeVisible({
        timeout: 10_000,
      });

      // ---- Malware → Overview ---------------------------------------------
      await gotoSecurity(page, "malware");
      await expect(page.getByText(/Stack health/i)).toBeVisible();
      for (const label of [
        "ClamAV daemon",
        "Freshclam",
        "Realtime monitor",
        "Tetragon",
      ]) {
        await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
      }

      // ---- Malware → all sub-tabs reachable -------------------------------
      for (const tab of [
        "Overview",
        "Quarantine",
        "Events",
        "Manual scan",
        "YARA rules",
        "Tetragon",
        "Settings",
      ]) {
        await expect(
          page.getByRole("tab", { name: new RegExp(`^${tab}$`, "i") }),
        ).toBeVisible();
      }

      // ---- Malware → Manual scan modes ------------------------------------
      await page.getByRole("tab", { name: /Manual scan/i }).click();
      await expect(
        page.locator(".ant-radio-button-wrapper").filter({ hasText: /^Path$/ }),
      ).toBeVisible();
      await expect(
        page.locator(".ant-radio-button-wrapper").filter({ hasText: /^Users$/ }),
      ).toBeVisible();
      await page
        .locator(".ant-radio-button-wrapper")
        .filter({ hasText: /^Users$/ })
        .click();
      await expect(
        page.getByRole("button", { name: /Scan selected/i }),
      ).toBeVisible();

      // ---- Firewall (UFW) --------------------------------------------------
      await gotoSecurity(page, "ufw");
      await expect(
        page.getByText(/Firewall is (enabled|disabled)|Add rule|UFW/i).first(),
      ).toBeVisible({ timeout: 15_000 });
    });
  });
}
