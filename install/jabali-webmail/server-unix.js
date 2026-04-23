#!/usr/bin/env node
// M25 Step 5 — Bulwark Next.js wrapper that listens on a Unix socket
// instead of TCP 127.0.0.1:3000.
//
// Decision (per plan task 1, option a): we wrap rather than fork Bulwark's
// stock /opt/jabali-webmail/server.js because the next bulwark update will
// overwrite server.js but leave server-unix.js alone. This wrapper reuses
// the same Next.js request handler via `next({})` — the documented
// custom-server pattern — with one extra step: priming
// __NEXT_PRIVATE_STANDALONE_CONFIG from the stock server.js so Next skips
// the loadWebpackHook step that throws in standalone bundles (the tarball
// strips node_modules/next/dist/compiled/webpack/, which loadWebpackHook
// eagerly require.resolves; config.js catches the throw ONLY when the
// standalone-config env var is set, otherwise rethrows → crash loop).
//
// Why a wrapper at all (and not, say, a `socat` UNIX↔TCP bridge): keeping
// Node single-process means we don't pay an extra hop on every request
// and we don't have a bridge crash unrelated to Bulwark itself. socat is
// fine in development; in production the systemd-managed Node process is
// the only thing in the box.
//
// Env contract:
//   SOCKET_PATH — absolute path to the unix socket. Required.
//   NODE_ENV    — set to "production" below to match stock server.js
//                 semantics (Next prepares the production server).
//
// Permissions: systemd creates /run/jabali-bulwark with mode 0750
// (RuntimeDirectory). Node's net.Server.listen uses the process umask
// for the socket file, so we explicitly fs.chmod after listen completes
// to land at 0660 — same convention used by panel-api/cmd/server/listener.go
// and Kratos's serve.public.socket.mode config.

const fs = require('fs');
const http = require('http');
const path = require('path');

const socketPath = process.env.SOCKET_PATH;
if (!socketPath) {
  console.error('[server-unix] SOCKET_PATH env var is required');
  process.exit(1);
}

// --- Prime standalone config BEFORE requiring next ---
// Stock server.js embeds the full resolved Next config as a JS object
// literal. We parse it out and re-publish it via the private env var
// Next reads at config-load time. Must happen before require('next').
const stockServerJsPath = path.join(__dirname, 'server.js');
try {
  const stockSrc = fs.readFileSync(stockServerJsPath, 'utf8');
  const match = stockSrc.match(/const\s+nextConfig\s*=\s*(\{[\s\S]*?\})\s*\n\s*process\.env\.__NEXT_PRIVATE_STANDALONE_CONFIG/);
  if (!match) {
    throw new Error('could not locate nextConfig literal in stock server.js');
  }
  // eslint-disable-next-line no-new-func
  const nextConfig = JSON.parse(match[1]);
  process.env.__NEXT_PRIVATE_STANDALONE_CONFIG = JSON.stringify(nextConfig);
} catch (err) {
  console.error(`[server-unix] failed to prime standalone config from ${stockServerJsPath}:`, err);
  process.exit(1);
}
process.env.NODE_ENV = 'production';

const next = require('next');

// Stale-socket cleanup. systemd's RuntimeDirectoryPreserve=no should
// already wipe /run/jabali-bulwark/ on stop, but defensive: if the unit
// crashed and the dir survived (rare), unlink the dangling socket so
// listen() doesn't fail with EADDRINUSE.
try {
  if (fs.existsSync(socketPath)) {
    fs.unlinkSync(socketPath);
  }
} catch (err) {
  console.error(`[server-unix] failed to clean stale socket ${socketPath}:`, err);
  process.exit(1);
}

// Bulwark's standalone build sets `dir` to its own root — in our deploy
// that's /opt/jabali-webmail (matches the systemd unit's WorkingDirectory).
// next() defaults to process.cwd(); systemd's WorkingDirectory= ensures
// that's correct without an explicit dir arg.
const app = next({ dev: false, dir: process.cwd() });
const handle = app.getRequestHandler();

app
  .prepare()
  .then(() => {
    const server = http.createServer((req, res) => handle(req, res));
    server.listen(socketPath, () => {
      try {
        // 0o660 = rw-rw---- — nginx (jabali-sockets group member) can
        // connect; nothing else on the host has reach.
        fs.chmodSync(socketPath, 0o660);
      } catch (err) {
        console.error(`[server-unix] chmod 0660 ${socketPath} failed:`, err);
        process.exit(1);
      }
      console.log(`[server-unix] Bulwark listening on unix:${socketPath}`);
    });

    // Graceful shutdown so systemd's TimeoutStopSec doesn't escalate to
    // SIGKILL during config reloads. Listening on Unix sockets means we
    // also want to clean the socket file on the way out.
    const shutdown = (sig) => {
      console.log(`[server-unix] received ${sig}, shutting down`);
      server.close(() => {
        try { fs.unlinkSync(socketPath); } catch (_) { /* already gone */ }
        process.exit(0);
      });
      // Hard cap: if Next.js never closes (websocket leak, etc.), still
      // exit so systemd doesn't SIGKILL us mid-flush.
      setTimeout(() => process.exit(0), 5000).unref();
    };
    process.on('SIGTERM', () => shutdown('SIGTERM'));
    process.on('SIGINT', () => shutdown('SIGINT'));
  })
  .catch((err) => {
    console.error('[server-unix] app.prepare failed:', err);
    process.exit(1);
  });
