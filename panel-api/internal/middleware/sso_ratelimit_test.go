package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSSOPhpMyAdminRateLimiter_Allow(t *testing.T) {
	limiter := NewSSOPhpMyAdminRateLimiter()

	// First request should be allowed
	allowed, retryAfter := limiter.Allow("user1")
	assert.True(t, allowed)
	assert.Equal(t, 0.0, retryAfter)

	// Immediate second request should be denied (burst=1)
	allowed, retryAfter = limiter.Allow("user1")
	assert.False(t, allowed)
	assert.Greater(t, retryAfter, 0.0)

	// Different user should have own limit
	allowed, retryAfter = limiter.Allow("user2")
	assert.True(t, allowed)
	assert.Equal(t, 0.0, retryAfter)
}

func TestSSOPhpMyAdminRateLimiter_Middleware_NoUserContext(t *testing.T) {
	limiter := NewSSOPhpMyAdminRateLimiter()
	r := gin.New()
	r.POST("/sso/phpmyadmin", limiter.Middleware(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// No user context set - should be denied
	req := httptest.NewRequest("POST", "/sso/phpmyadmin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var respBody map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &respBody)
	assert.Equal(t, "rate_limited", respBody["error"])
}

func TestSSOPhpMyAdminRateLimiter_Middleware_RateLimited(t *testing.T) {
	limiter := NewSSOPhpMyAdminRateLimiter()

	// Helper to hit the endpoint with user_id
	hit := func(userID string) (int, map[string]interface{}) {
		r := gin.New()
		r.POST("/sso/phpmyadmin",
			func(c *gin.Context) { c.Set("user_id", userID) },
			limiter.Middleware(),
			func(c *gin.Context) { c.String(http.StatusOK, "ok") },
		)
		req := httptest.NewRequest("POST", "/sso/phpmyadmin", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var respBody map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &respBody)
		return w.Code, respBody
	}

	// First request should pass
	code1, _ := hit("user1")
	assert.Equal(t, http.StatusOK, code1, "first request should be allowed")

	// Second request should be rate limited
	code2, respBody2 := hit("user1")
	assert.Equal(t, http.StatusTooManyRequests, code2)
	assert.Equal(t, "rate_limited", respBody2["error"])
	assert.Greater(t, respBody2["retry_after_seconds"], float64(0))
}

func TestSSOPhpMyAdminRateLimiter_Cleanup(t *testing.T) {
	limiter := NewSSOPhpMyAdminRateLimiter()

	// Create some buckets
	limiter.Allow("user1")
	limiter.Allow("user2")

	// Cleanup with a short maxAge should remove them all
	// (This is a simple test; in production we'd need time travel)
	limiter.Cleanup(0)

	// After cleanup, users should get new buckets
	allowed, _ := limiter.Allow("user1")
	assert.True(t, allowed)
	allowed, _ = limiter.Allow("user1") // second call should be rate limited
	assert.False(t, allowed)
}
