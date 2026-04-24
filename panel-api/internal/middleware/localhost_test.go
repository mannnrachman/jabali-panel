package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", RequireLocalhost(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func requestWithRemote(t *testing.T, r *gin.Engine, remote string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRequireLocalhost_LoopbackIPv4(t *testing.T) {
	if code := requestWithRemote(t, setupRouter(), "127.0.0.1:54321"); code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestRequireLocalhost_LoopbackIPv6(t *testing.T) {
	if code := requestWithRemote(t, setupRouter(), "[::1]:54321"); code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
}

func TestRequireLocalhost_UnixSocketAtSentinel(t *testing.T) {
	// net/http sets RemoteAddr="@" for unix-socket peers.
	if code := requestWithRemote(t, setupRouter(), "@"); code != http.StatusOK {
		t.Fatalf("want 200 for unix-socket peer, got %d", code)
	}
}

func TestRequireLocalhost_UnixSocketEmpty(t *testing.T) {
	// Some adapters set RemoteAddr="" instead of "@" for unix-socket peers.
	if code := requestWithRemote(t, setupRouter(), ""); code != http.StatusOK {
		t.Fatalf("want 200 for empty-RemoteAddr unix-socket peer, got %d", code)
	}
}

func TestRequireLocalhost_RejectsPublicIPv4(t *testing.T) {
	if code := requestWithRemote(t, setupRouter(), "8.8.8.8:54321"); code != http.StatusForbidden {
		t.Fatalf("want 403 for public IP, got %d", code)
	}
}

func TestRequireLocalhost_RejectsPrivateIPv4(t *testing.T) {
	if code := requestWithRemote(t, setupRouter(), "10.0.0.5:54321"); code != http.StatusForbidden {
		t.Fatalf("want 403 for non-loopback private IP, got %d", code)
	}
}
