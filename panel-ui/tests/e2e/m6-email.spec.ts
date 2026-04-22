// M6: Email (Stalwart + Bulwark + per-mailbox SSO) E2E test.
//
// Covers the UI-driven control-plane flow:
//   1. Login as admin
//   2. Open a domain's Edit page, flip Email on, observe DKIM selector
//      + DNS autoconfig records rendered
//   3. Create a mailbox, capture the reveal-once password, verify the
//      row appears in the list
//   4. Click the "Webmail" button, confirm a new tab opens to
//      https://mail.<domain>/sso/webmail?token=... — landing server-
//      side verification lives in panel-api integration tests, this
//      spec proves the UI produces the token-URL handoff correctly.
//   5. Delete the mailbox + flip email off; verify the rows go away
//
// SMTP/IMAP loopback delivery (Gate #2/#3 in the blueprint) is NOT
// covered here — LXC containers block RFC1918 25/465/587 outbound, so
// the test would be unreliable across host topologies. The manual
// runbook at plans/m6-email-runbook.md documents the deferred check.
//
// To run locally:
//   export E2E_BASE_URL=https://jabali-panel.local
//   export E2E_USERNAME=admin@example.com
//   export E2E_PASSWORD=...
//   export E2E_M6_DOMAIN=jabali-probe.example.com   # existing owned domain
//   npm run test:e2e m6-email.spec.ts

import { expect, test } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME || !process.env.E2E_PASSWORD || !process.env.E2E_M6_DOMAIN
    ? "E2E_USERNAME, E2E_PASSWORD, E2E_M6_DOMAIN not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `M6 E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M6: Email (admin flow)", () => {
    test.setTimeout(120_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";
    const domain = process.env.E2E_M6_DOMAIN || "";

    async function login(
      page: import("@playwright/test").Page,
    ): Promise<void> {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-panel/);
    }

    test("enable → create mailbox → webmail SSO url → delete → disable", async ({
      page,
      context,
    }) => {
      await login(page);

      // Find the probe domain in the admin Domains list and open its
      // Edit page. The list renders server-side so we navigate to the
      // route directly via the name-search params.
      await page.goto("/jabali-panel/domains");
      const row = page.getByRole("row", { name: new RegExp(domain, "i") }).first();
      await expect(row).toBeVisible();
      await row.getByRole("link", { name: /edit|view|open/i }).first().click();

      await page.waitForURL(/\/jabali-panel\/domains\/[A-Z0-9]{26}/i);

      // Email tab is labelled "Email" in the tab-strip. The panel's
      // DomainEmailSection contains a Switch — click-to-enable. After
      // the POST /domains/:id/email round-trips we expect the DKIM
      // fields + records table to render.
      await page.getByRole("tab", { name: /^email$/i }).click();

      const emailSwitch = page.locator("button[role='switch']").first();
      if ((await emailSwitch.getAttribute("aria-checked")) !== "true") {
        await emailSwitch.click();
        // Toast: success message appears; then the DNS records table
        // materialises. Poll for the autoconfig CNAME row.
        await expect(
          page.getByText("autoconfig.", { exact: false }).first(),
        ).toBeVisible({ timeout: 15_000 });
      }

      // Mailboxes tab: create alice with an auto-generated password.
      await page.getByRole("tab", { name: /^mailboxes$/i }).click();

      const ts = Date.now().toString(36);
      const localPart = `e2e-${ts}`;
      const fullEmail = `${localPart}@${domain}`;

      await page.getByRole("button", { name: /new mailbox|create mailbox/i }).click();
      await page.getByLabel(/local part/i).fill(localPart);
      // Leave the password field empty so the server generates one.
      await page.getByRole("button", { name: /^create$/i }).click();

      // Reveal-once modal shows the password. We don't need the value —
      // just assert it appears (shape: 26 chars of Crockford base32).
      const passwordModal = page.getByRole("dialog");
      await expect(passwordModal).toBeVisible();
      const revealCode = passwordModal
        .locator("code, pre, input[type='text']")
        .first();
      await expect(revealCode).toContainText(/[0-9A-Z]{10,}/);

      // Close the reveal modal.
      const doneBtn = passwordModal.getByRole("button", { name: /done|close|ok/i });
      if (await doneBtn.isVisible().catch(() => false)) {
        await doneBtn.click();
      } else {
        await page.keyboard.press("Escape");
      }

      // The new row must appear in the table.
      const mailboxRow = page.getByRole("row", { name: new RegExp(fullEmail, "i") });
      await expect(mailboxRow).toBeVisible({ timeout: 10_000 });

      // Webmail SSO: clicking the button pops a new tab. We intercept
      // the mint request to capture the URL rather than try to drive
      // Bulwark through headless Chromium (which needs extra cert
      // trust + a network path to mail.<domain>).
      const mintResponsePromise = page.waitForResponse(
        (resp) =>
          resp.url().includes(`/mailboxes/`) &&
          resp.url().endsWith("/sso") &&
          resp.status() === 200,
      );
      const [newTab] = await Promise.all([
        context.waitForEvent("page"),
        mailboxRow.getByRole("button", { name: /webmail|open webmail/i }).click(),
      ]);
      const mintResp = await mintResponsePromise;
      const mintJson = await mintResp.json();
      expect(mintJson).toHaveProperty("url");
      expect(mintJson.url).toMatch(
        new RegExp(`^https://mail\\.${domain}/sso/webmail\\?token=`),
      );
      expect(mintJson).toHaveProperty("mail_host", `mail.${domain}`);

      // Close the Bulwark tab without driving it — the handshake is
      // server-side and covered by panel-api integration tests.
      await newTab.close();

      // Delete the mailbox.
      await mailboxRow.getByRole("button", { name: /delete|remove/i }).click();
      const confirmBtn = page
        .getByRole("button", { name: /^delete$/i, exact: false })
        .last();
      await confirmBtn.click();
      await expect(mailboxRow).toBeHidden({ timeout: 10_000 });

      // Disable email. After the DELETE /domains/:id/email the Switch
      // flips back to off and the records table stays visible only for
      // whatever is live (the M6 DNS rows are gone — tested here
      // indirectly via the Switch flipping).
      await page.getByRole("tab", { name: /^email$/i }).click();
      if ((await emailSwitch.getAttribute("aria-checked")) === "true") {
        await emailSwitch.click();
        await expect(emailSwitch).toHaveAttribute("aria-checked", "false", {
          timeout: 10_000,
        });
      }
    });
  });
}
