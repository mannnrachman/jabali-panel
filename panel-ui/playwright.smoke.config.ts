import { defineConfig, devices } from "@playwright/test";

/**
 * Smoke test config - runs against the live panel server at
 * https://mx.jabali-panel.local:8443 with self-signed cert.
 * 
 * Usage:
 *   npx playwright test --config=playwright.smoke.config.ts
 */
export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false, // Sequential for smoke tests
  forbidOnly: !!process.env.CI,
  retries: 1,
  workers: 1,
  reporter: [["list"], ["html", { open: "never" }]],

  use: {
    baseURL: "https://mx.jabali-panel.local:8443",
    // Ignore self-signed certificate errors
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    // Slower actions for stability
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
  },

  projects: [
    {
      name: "chromium-smoke",
      use: { 
        ...devices["Desktop Chrome"],
        // Additional chromium args for self-signed certs
        launchOptions: {
          args: [
            "--ignore-certificate-errors",
            "--ignore-certificate-errors-spki-list",
            "--allow-insecure-localhost",
          ],
        },
      },
    },
  ],
});
