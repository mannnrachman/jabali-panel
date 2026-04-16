// Package panelui exposes the built SPA (panel-ui/dist) as a filesystem
// that the Go panel-api can serve from HTTP.
//
// Why a Go file in a Node project: //go:embed cannot traverse parent
// directories (no "..dist" style), so the embed directive must live in
// a package sibling to the files it's embedding. Nothing else in this
// directory cares — `go build` ignores non-.go files, and `npm run
// build` / Vite ignore Go files. Both toolchains coexist cleanly.
//
// The embedded tree comes from `npm run build` in this directory. The
// committed dist/index.html placeholder is enough to make `go build`
// succeed on a fresh clone before the SPA has been built.
package panelui

import (
	"embed"
	"io/fs"
)

// all:dist pulls in dist/.gitkeep too, which is the minimum content we
// guarantee exists on a fresh clone. After `npm run build` the directory
// holds real index.html + /assets/*; before, it's just the sentinel.
//
//go:embed all:dist
var embedded embed.FS

// Assets returns an fs.FS rooted at the built dist/ directory — callers
// see the SPA's assets directly without the "dist/" prefix.
func Assets() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Impossible unless someone deletes dist/ after compile; the embed
		// would have failed at build time.
		panic("panel-ui: embedded dist missing: " + err.Error())
	}
	return sub
}
