package commands

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// renderVhostForCacheTest renders the vhost template the same way
// writeVhost does, with the cache toggle as the only variable.
func renderVhostForCacheTest(t *testing.T, cacheEnabled bool) string {
	t.Helper()
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	vd := vhostData{
		Domain:     "example.com",
		DocRoot:    "/home/u/public_html/example.com",
		HasPHP:     true,
		PHPVersion: "8.3",
		Username:   "u",
		IsEnabled:  true,
		// matches writeVhost's hardcoded values
		CacheEnabled: cacheEnabled,
		CacheKeyZone: "jabali_fcgi",
		CacheTTL:     "60s",
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vd); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	return buf.String()
}

var cacheMarkers = []string{
	"fastcgi_cache jabali_fcgi;",
	"fastcgi_cache_key",
	"fastcgi_cache_valid 200 301 302 60s;",
	"fastcgi_cache_bypass $jabali_skip;",
	"fastcgi_no_cache $jabali_skip;",
	"add_header X-Jabali-Cache $upstream_cache_status always;",
	"set $jabali_skip 0;",
	"wordpress_logged_in",
	"location ~* \\.(?:css|js|jpe?g|png|gif|ico|svg|webp|woff2?|ttf|eot)$",
}

// ADR-0108: cache OFF must emit NONE of the cache/static directives so
// behaviour is identical to the pre-0108 vhost; cache ON must emit all
// of them inside the PHP location. This is the load-bearing safety
// guard for the per-domain toggle.
func TestVhost_CacheDisabled_NoCacheDirectives(t *testing.T) {
	t.Parallel()
	out := renderVhostForCacheTest(t, false)
	for _, m := range cacheMarkers {
		if strings.Contains(out, m) {
			t.Errorf("cache OFF must not emit %q\n---\n%s", m, out)
		}
	}
	// The normal PHP path must still be intact when cache is off.
	if !strings.Contains(out, "fastcgi_pass unix:/run/php/jabali-u/fpm.sock;") {
		t.Errorf("cache OFF broke the normal PHP location:\n%s", out)
	}
}

func TestVhost_CacheEnabled_AllDirectivesPresent(t *testing.T) {
	t.Parallel()
	out := renderVhostForCacheTest(t, true)
	for _, m := range cacheMarkers {
		if !strings.Contains(out, m) {
			t.Errorf("cache ON must emit %q\n---\n%s", m, out)
		}
	}
	// Cache directives must sit inside the PHP location, after the
	// fastcgi_pass (so they apply to the FastCGI response).
	pass := strings.Index(out, "fastcgi_pass unix:")
	cache := strings.Index(out, "fastcgi_cache jabali_fcgi;")
	if pass < 0 || cache < 0 || cache < pass {
		t.Errorf("fastcgi_cache must appear after fastcgi_pass (pass=%d cache=%d)", pass, cache)
	}
}
