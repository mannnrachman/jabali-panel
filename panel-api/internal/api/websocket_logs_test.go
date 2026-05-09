package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// CheckOrigin gates every log-stream WebSocket handshake. Bug here =
// CSRF-via-WS escape valve, so the matrix is exhaustive.

func mkReq(t *testing.T, host, origin string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/logs/stream/abc", nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestCheckLogStreamOrigin_AllowsMissingHeader(t *testing.T) {
	// Native WS clients (curl, ws CLI, panel-api self-call) don't send
	// Origin. They can't be CSRF'd because there's no browser ambient
	// credential to leak — allow.
	if !checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "")) {
		t.Fatal("missing Origin should be allowed")
	}
}

func TestCheckLogStreamOrigin_AllowsSameOriginHTTPS(t *testing.T) {
	if !checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "https://panel.example.com:8443")) {
		t.Fatal("same-host origin should be allowed")
	}
}

func TestCheckLogStreamOrigin_AllowsSameOriginHTTP(t *testing.T) {
	// Dev http listener — scheme not compared, only host:port.
	if !checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "http://panel.example.com:8443")) {
		t.Fatal("same-host origin (http scheme) should be allowed")
	}
}

func TestCheckLogStreamOrigin_RejectsCrossHost(t *testing.T) {
	if checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "https://attacker.example.com")) {
		t.Fatal("cross-host origin must be rejected")
	}
}

func TestCheckLogStreamOrigin_RejectsCrossPort(t *testing.T) {
	// Panel on :8443; origin claims :443 — same hostname but different
	// listener. Could be a co-tenant on the same FQDN exfiltrating.
	if checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "https://panel.example.com:443")) {
		t.Fatal("cross-port origin must be rejected")
	}
}

func TestCheckLogStreamOrigin_RejectsSubdomain(t *testing.T) {
	// Subdomain (`api.panel.example.com`) is NOT same-origin even
	// though it shares the parent domain — browsers treat them as
	// distinct origins.
	if checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", "https://api.panel.example.com:8443")) {
		t.Fatal("subdomain origin must be rejected")
	}
}

func TestCheckLogStreamOrigin_RejectsMalformedOrigin(t *testing.T) {
	// "null" (sandboxed iframe), "https://" (no host), random garbage —
	// none should pass.
	for _, bad := range []string{
		"null",
		"https://",
		"javascript:alert(1)",
		"file://",
		"data:text/html,",
	} {
		if checkLogStreamOrigin(mkReq(t, "panel.example.com:8443", bad)) {
			t.Errorf("malformed origin %q must be rejected", bad)
		}
	}
}
