// Package webui serves the built React SPA (panel-ui/dist) from the Go
// HTTP server. Same origin as the API so the SameSite=Strict refresh
// cookie flows without CORS gymnastics.
//
// Routing rules:
//   - Requests with a known extension (.js/.css/.png/...) map directly
//     to the embedded file, or 404 if missing.
//   - Everything else falls back to index.html so React Router deep
//     links (/users/01K..., /settings/ssl, etc) render the SPA shell.
//   - The mount is intentionally registered LAST so API handlers
//     (/health, /api/v1/*, /ws/*) take precedence.
package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterStatic wires the SPA into the given gin.Engine. The panelFS
// is an fs.FS rooted at the built dist/ (see panel-ui.Assets).
//
// We use NoRoute rather than g.StaticFS + explicit wildcards so API
// 404s keep returning JSON — only truly unrouted paths land on the SPA.
// Paths that start with an "api-ish" prefix still 404 as JSON so a
// typo'd API URL doesn't silently serve the SPA shell.
func RegisterStatic(g *gin.Engine, panelFS fs.FS) {
	fileServer := http.FileServer(http.FS(panelFS))
	hasUI := fileExists(panelFS, "index.html")

	g.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path

		// API-ish paths must never masquerade as SPA routes — a misspelled
		// endpoint should 404 with JSON, not render index.html.
		if looksLikeAPI(p) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}

		// Fresh-clone / "npm run build didn't run" case: dist only has
		// the .gitkeep sentinel. Serve a short explainer so the reader
		// knows exactly what to do, instead of a bare 404.
		if !hasUI {
			c.Data(http.StatusOK, "text/html; charset=utf-8", notBuiltPage)
			return
		}

		// If the path resolves to a real file in the embed, serve it.
		// Otherwise fall back to index.html so client-side routing can
		// take over.
		isFallback := p == "/" || !fileExists(panelFS, strings.TrimPrefix(p, "/"))
		if isFallback {
			c.Request.URL.Path = "/"
			// SPA shell must revalidate on every load. Vite hashes asset
			// filenames so JS/CSS can be cached forever, but index.html
			// references those hashes — a cached shell after a deploy
			// points at 404'd assets. no-cache forces an If-None-Match
			// round-trip so the browser picks up the new shell.
			c.Header("Cache-Control", "no-cache")
			// Users landing on /login are either post-logout or
			// post-token-expiry. In both cases their cached /api/v1/me
			// and /auth/refresh responses are useless (or actively
			// harmful — Firefox attaches OpaqueResponseBlocking
			// decisions to cached 401s and replays them, producing a
			// blank page). Nuke the cache so the fresh login flow
			// starts from a clean slate. Storage (JWT/refresh token
			// state) is preserved — only HTTP cache is cleared.
			if p == "/login" {
				c.Header("Clear-Site-Data", `"cache"`)
			}
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

// notBuiltPage is the sentinel rendered when the SPA hasn't been built —
// i.e. the Go binary compiled but `npm run build` was skipped. Operators
// get a specific next step rather than a generic 404.
var notBuiltPage = []byte(`<!doctype html>
<html lang="en"><head><meta charset="UTF-8"><title>Jabali Panel — UI not built</title>
<style>body{font-family:system-ui;padding:2rem;max-width:40rem;margin:auto;color:#333}</style>
</head><body>
<h1>Jabali Panel</h1>
<p>The SPA hasn't been built yet. On the server:</p>
<pre>cd /opt/jabali-panel/panel-ui && npm ci && npm run build
systemctl restart jabali-panel</pre>
<p>Or run <code>install.sh</code> again — it does this for you.</p>
</body></html>`)

// looksLikeAPI is a conservative check — if we add more API surfaces
// later (GraphQL, gRPC-web), extend this list. The SPA never has
// top-level paths named after these prefixes.
func looksLikeAPI(p string) bool {
	switch {
	case strings.HasPrefix(p, "/api/"):
		return true
	case strings.HasPrefix(p, "/health"):
		return true
	case strings.HasPrefix(p, "/ws/"):
		return true
	}
	return false
}

// fileExists returns true if path p resolves to a regular file (not a
// directory) inside the embedded FS.
func fileExists(panelFS fs.FS, p string) bool {
	if p == "" {
		return false
	}
	// Clean the path so `..` or doubled slashes can't escape the embed.
	p = path.Clean(p)
	f, err := panelFS.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return !info.IsDir()
}
