// Playwright config for panel-ui.
//
// Default mode: spins up Vite's `preview` command (built SPA served as
// production assets) and drives it through headless Chromium. The tests
// mock /api/* responses with route.fulfill(), so no backend is required
// in CI or on a fresh developer machine.
//
// A "live" mode for running against a real deployment can be added later
// by branching on an env var; not worth the YAML yet with one use case.
import { defineConfig, devices } from "@playwright/test";

const PORT = 4173;

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 15_000,
  expect: { timeout: 4_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",

  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  // Serve the production build locally so we're testing what ships, not
  // dev-mode vite with HMR + extra magic. `npm run build` is expected
  // to have already run; `vite preview` just serves dist/.
  //
  // Timeout 120s (not 30s): preview boots in ~5s locally, but the
  // self-hosted Gitea act_runner is on a constrained host and has
  // already timed out at 30s once (run #22 — docs-blueprint-m20-sync).
  // stdout: "pipe" so the server's "Local: http://…" line is visible
  // in the job log — next time it times out, we'll know why.
  webServer: {
    command: `npm run preview -- --port ${PORT} --host 127.0.0.1`,
    url: `http://127.0.0.1:${PORT}`,
    reuseExistingServer: !process.env.CI,
    stdout: "pipe",
    stderr: "pipe",
    timeout: 120_000,
  },
});
