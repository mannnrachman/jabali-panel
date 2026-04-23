/// <reference types="vitest" />
// Jabali Panel SPA — Vite config.
//
// Dev mode: runs on :5173 with a /api proxy into the panel-api on :8443.
// Routing everything through one origin keeps the refresh cookie
// (SameSite=Strict) flowing; a raw cross-origin XHR would silently drop it.
//
// Prod mode: `vite build` emits static files into ./dist, which the Go
// panel-api embeds via //go:embed and serves from / with SPA fallback.
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// Default proxy target — override with VITE_API_PROXY_TARGET when running
// against something other than a local panel-api (e.g. the test VM).
const DEFAULT_API_TARGET = "http://127.0.0.1:8443";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.VITE_API_PROXY_TARGET || DEFAULT_API_TARGET;

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@icons": "/src/icons",
      },
    },
    server: {
      // Bind to 0.0.0.0 so a dev running inside a VM can be reached from
      // the host; harmless on a laptop because OSX/Linux firewalls gate it.
      host: "0.0.0.0",
      port: 5173,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          // We keep the path; the panel expects /api/v1/… as-is.
        },
        "/health": { target: apiTarget, changeOrigin: true },
      },
    },
    test: {
      globals: true,
      environment: "happy-dom",
      setupFiles: ["./src/test/setup.ts"],
      // Exclude Playwright E2E tests — those run via `npx playwright test`.
      exclude: ["tests/e2e/**", "node_modules/**"],
      css: false,
    },
    build: {
      // Single bundle. Earlier attempts to manually split react / antd /
      // refine into named chunks tripped on init-order bugs: antd was
      // calling React.createContext before the 'vendor' chunk that held
      // react had finished loading, which crashed the app on boot with
      // "Cannot read properties of undefined (reading 'createContext')".
      // The caching win from splitting would've been real, but shipping
      // a silently-broken SPA isn't worth chasing it. Revisit when we
      // either move to lazy route chunks or adopt a vendor-import-map.
      chunkSizeWarningLimit: 1800,
    },
  };
});
