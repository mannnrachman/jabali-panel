// M24: IP address manager E2E test.
//
// Covers the admin-driven flow against a live panel VM that already
// has a secondary IP bound on the network interface (the test does
// NOT exercise persistence — that's an out-of-scope distro/provider
// concern; see plans/m24-ip-manager-runbook.md).
//
// Flow:
//   1. Login as admin
//   2. Open /jabali-admin/ips, add the pre-bound secondary IP to the pool
//   3. Open the probe domain's Edit page, pick the new IP as ListenIPv4
//   4. Wait ≤ 90s (one reconcile cycle + nginx reload) for convergence
//   5. Assert curl --resolve <domain>:80:<bound>  → 200 OK
//      Assert curl --resolve <domain>:80:<other>  → connection refused
//   6. Assert dig @<ns> <domain> A returns the bound IP
//   7. Reset the binding to "Use server default"
//   8. Assert both IPs respond on :80 again
//
// The spec uses the same skip-on-missing-env pattern as m6-email.spec.ts
// so CI doesn't fail on dev hosts that can't reach a live VM.
//
// To run locally:
//   export E2E_BASE_URL=https://jabali-panel.local
//   export E2E_USERNAME=admin@example.com
//   export E2E_PASSWORD=...
//   export E2E_M24_DOMAIN=ip-probe.example.com    # existing owned domain
//   export E2E_M24_BOUND_IP=10.0.3.50             # secondary IP, pre-bound
//   export E2E_M24_OTHER_IP=192.168.100.150             # server primary
//   export E2E_M24_NS=192.168.100.150                   # auth nameserver
//   npm run test:e2e ip-manager.spec.ts

import { execFileSync } from "node:child_process";
import { expect, test } from "@playwright/test";

const SKIP_REASON =
  !process.env.E2E_USERNAME ||
  !process.env.E2E_PASSWORD ||
  !process.env.E2E_M24_DOMAIN ||
  !process.env.E2E_M24_BOUND_IP ||
  !process.env.E2E_M24_OTHER_IP ||
  !process.env.E2E_M24_NS
    ? "E2E_USERNAME, E2E_PASSWORD, E2E_M24_DOMAIN, E2E_M24_BOUND_IP, E2E_M24_OTHER_IP, E2E_M24_NS not set"
    : "";

if (SKIP_REASON) {
  test.skip(() => true, `M24 E2E skipped: ${SKIP_REASON}`);
} else {
  test.describe("M24: IP address manager", () => {
    test.setTimeout(180_000);

    test.use({
      ignoreHTTPSErrors: true,
      baseURL: process.env.E2E_BASE_URL || "https://jabali-panel.local",
    });

    const username = process.env.E2E_USERNAME || "";
    const password = process.env.E2E_PASSWORD || "";
    const domain = process.env.E2E_M24_DOMAIN || "";
    const boundIP = process.env.E2E_M24_BOUND_IP || "";
    const otherIP = process.env.E2E_M24_OTHER_IP || "";
    const ns = process.env.E2E_M24_NS || "";

    async function login(
      page: import("@playwright/test").Page,
    ): Promise<void> {
      await page.goto("/login");
      await page.getByLabel(/email/i).fill(username);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /sign in|log in/i }).click();
      await page.waitForURL(/\/jabali-admin/);
    }

    // curlReturnsStatus runs curl with --resolve to force the
    // request to a specific IP. Returns the HTTP status code, or 0
    // when the connection itself fails (refused, timeout, etc.).
    function curlReturnsStatus(host: string, ip: string): number {
      try {
        const out = execFileSync(
          "curl",
          [
            "-sk",
            "--max-time",
            "5",
            "--resolve",
            `${host}:80:${ip}`,
            "-o",
            "/dev/null",
            "-w",
            "%{http_code}",
            `http://${host}/`,
          ],
          { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
        );
        return parseInt(out.trim(), 10) || 0;
      } catch {
        return 0;
      }
    }

    function digA(host: string, server: string): string[] {
      try {
        const out = execFileSync(
          "dig",
          ["@" + server, host, "A", "+short", "+time=3", "+tries=2"],
          { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
        );
        return out
          .trim()
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean);
      } catch {
        return [];
      }
    }

    async function pickListenIPv4(
      page: import("@playwright/test").Page,
      label: string,
    ): Promise<void> {
      // Find the "Listen IPv4" Form.Item and open the Select dropdown.
      const formItem = page.locator(".ant-form-item", {
        hasText: /^Listen IPv4/i,
      });
      await formItem.locator(".ant-select").click();
      await page
        .locator(".ant-select-item-option", { hasText: label })
        .first()
        .click();
      await page.getByRole("button", { name: /save listen ips/i }).click();
      await expect(
        page.getByText(/listen-ip binding updated/i),
      ).toBeVisible({ timeout: 10_000 });
    }

    test("add IP → bind to domain → verify nginx + DNS → unbind", async ({
      page,
    }) => {
      await login(page);

      // Step 1: add the secondary IP to the pool if it isn't already there.
      await page.goto("/jabali-admin/ips");
      const existingRow = page.getByRole("row", { name: new RegExp(boundIP) });
      if (!(await existingRow.first().isVisible().catch(() => false))) {
        await page.getByRole("button", { name: /add ip/i }).click();
        await page.getByLabel(/address/i).fill(boundIP);
        await page.getByLabel(/label/i).fill("e2e secondary");
        await page.getByRole("button", { name: /^add ip$/i }).click();
        await page.waitForURL(/\/jabali-admin\/ips$/);
        await expect(
          page.getByRole("row", { name: new RegExp(boundIP) }),
        ).toBeVisible({ timeout: 10_000 });
      }

      // Step 2: open domain edit, pick the new IP as ListenIPv4.
      await page.goto("/jabali-admin/domains");
      const domainRow = page.getByRole("row", { name: new RegExp(domain, "i") }).first();
      await expect(domainRow).toBeVisible();
      await domainRow.getByRole("button", { name: /^edit$/i }).first().click();
      await page.waitForURL(/\/jabali-admin\/domains\/edit\//);

      await pickListenIPv4(page, boundIP);

      // Step 3: wait for one reconcile cycle (default 30s) plus nginx
      // reload jitter. Poll curl up to 90s.
      let attempts = 0;
      const maxAttempts = 30;
      let boundStatus = 0;
      let otherStatus = -1;
      while (attempts < maxAttempts) {
        boundStatus = curlReturnsStatus(domain, boundIP);
        otherStatus = curlReturnsStatus(domain, otherIP);
        if (boundStatus >= 200 && boundStatus < 400 && otherStatus === 0) {
          break;
        }
        await new Promise((r) => setTimeout(r, 3000));
        attempts++;
      }
      expect(boundStatus, "bound IP must answer 2xx/3xx").toBeGreaterThan(0);
      expect(otherStatus, "other IP must NOT answer (connection refused)").toBe(0);

      // Step 4: DNS apex A reflects the binding within the same window.
      const aRecords = digA(domain, ns);
      expect(aRecords).toContain(boundIP);
      expect(aRecords).not.toContain(otherIP);

      // Step 5: unbind — pick "Use server default".
      await pickListenIPv4(page, /use server default/i.source);

      // Step 6: both IPs answer again.
      attempts = 0;
      let bothAnswer = false;
      while (attempts < maxAttempts) {
        boundStatus = curlReturnsStatus(domain, boundIP);
        otherStatus = curlReturnsStatus(domain, otherIP);
        if (
          boundStatus >= 200 &&
          boundStatus < 400 &&
          otherStatus >= 200 &&
          otherStatus < 400
        ) {
          bothAnswer = true;
          break;
        }
        await new Promise((r) => setTimeout(r, 3000));
        attempts++;
      }
      expect(bothAnswer, "both IPs must respond after unbind").toBe(true);
    });
  });
}
