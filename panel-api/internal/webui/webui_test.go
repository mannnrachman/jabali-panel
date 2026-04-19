package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func newTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fs := fstest.MapFS{
		"index.html":               {Data: []byte("<html></html>")},
		"assets/index-abc.js":      {Data: []byte("console.log('hi')")},
	}
	RegisterStatic(r, fs)
	return r
}

func TestSPAFallbackSetsNoCache(t *testing.T) {
	r := newTestEngine(t)

	// Deep link — no file match, fallback serves index.html.
	for _, path := range []string{"/", "/domains", "/settings/ssl"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("path %q: Cache-Control = %q, want %q", path, got, "no-cache")
		}
		if got := w.Header().Get("Clear-Site-Data"); got != "" {
			t.Errorf("path %q: Clear-Site-Data = %q, want empty (non-login path)", path, got)
		}
	}
}

func TestLoginFallbackSetsClearSiteData(t *testing.T) {
	r := newTestEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
	want := `"cache"`
	if got := w.Header().Get("Clear-Site-Data"); got != want {
		t.Errorf("Clear-Site-Data = %q, want %q", got, want)
	}
}

func TestAssetResponseHasNoCacheControl(t *testing.T) {
	// Real static assets (hashed .js/.css) must NOT get no-cache —
	// Vite hashes filenames specifically so they can be cached forever.
	r := newTestEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/index-abc.js", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Cache-Control"); got != "" {
		t.Errorf("static asset got Cache-Control = %q, want empty (browser default cacheable)", got)
	}
	if got := w.Header().Get("Clear-Site-Data"); got != "" {
		t.Errorf("static asset got Clear-Site-Data = %q, want empty", got)
	}
}

func TestAPIPathIs404JSON(t *testing.T) {
	r := newTestEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/bogus", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct == "" || ct[:16] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json...", ct)
	}
}
