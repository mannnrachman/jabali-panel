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
    build: {
      // Smaller chunks: AntD is huge, better to split so caching helps on
      // subsequent navs. 600kb cap matches vite's default warning.
      chunkSizeWarningLimit: 600,
    },
  };
});
