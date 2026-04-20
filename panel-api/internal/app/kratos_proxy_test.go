package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterKratosProxy_StripsPrefixAndForwards(t *testing.T) {
	// Fake Kratos: capture path + method + body + response cookies.
	var gotPath, gotMethod string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		// Emit a Kratos-style Set-Cookie to confirm pass-through.
		http.SetCookie(w, &http.Cookie{Name: "csrf_token_abc", Value: "xyz", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"f","ui":{"action":"","method":"POST","nodes":[]}}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := RegisterKratosProxy(r, upstream.URL); err != nil {
		t.Fatalf("RegisterKratosProxy: %v", err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	// GET: flow init path.
	resp, err := http.Get(srv.URL + "/.ory/self-service/login/browser")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if gotMethod != "GET" {
		t.Errorf("method=%q, want GET", gotMethod)
	}
	if gotPath != "/self-service/login/browser" {
		t.Errorf("path=%q, want /self-service/login/browser (prefix must be stripped)", gotPath)
	}
	// Set-Cookie from Kratos must come through — the SPA's CSRF handling
	// depends on it.
	if cookies := resp.Cookies(); len(cookies) == 0 || cookies[0].Name != "csrf_token_abc" {
		t.Errorf("expected csrf_token_abc cookie in response, got %+v", cookies)
	}

	// POST: flow submit path with body.
	body := strings.NewReader(`identifier=a&password=b`)
	resp2, err := http.Post(
		srv.URL+"/.ory/self-service/login?flow=abc",
		"application/x-www-form-urlencoded",
		body,
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp2.Body.Close()
	if gotMethod != "POST" {
		t.Errorf("method=%q, want POST", gotMethod)
	}
	if gotPath != "/self-service/login" {
		t.Errorf("path=%q, want /self-service/login (query preserved separately)", gotPath)
	}
	if string(gotBody) != "identifier=a&password=b" {
		t.Errorf("body=%q, want identifier=a&password=b", gotBody)
	}
}

func TestRegisterKratosProxy_RejectsBadURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// url.Parse is lenient — truly malformed URLs (control chars in host)
	// trip it. Use that to confirm the error path returns rather than panics.
	err := RegisterKratosProxy(r, "http://\x00bad")
	if err == nil {
		t.Fatal("expected error for malformed upstream URL, got nil")
	}
}
