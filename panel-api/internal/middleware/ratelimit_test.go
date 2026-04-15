package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

func TestRateLimit_BlocksAfterBurst(t *testing.T) {
	t.Parallel()

	// 1 req/sec with a burst of 2 → the third request back-to-back should
	// be rejected before the rate replenishes.
	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		DefaultRate:  rate.Limit(1),
		DefaultBurst: 2,
	})
	r := gin.New()
	r.GET("/x", rl.Default(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	hit := func() int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:54321"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, hit())
	assert.Equal(t, http.StatusOK, hit())

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	// Retry-After must be a non-negative integer.
	ra, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err, "Retry-After must be an integer")
	assert.GreaterOrEqual(t, ra, 0)
}

func TestRateLimit_SeparateBucketsPerIP(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		DefaultRate:  rate.Limit(1),
		DefaultBurst: 1,
	})
	r := gin.New()
	r.GET("/x", rl.Default(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	hit := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = ip + ":1000"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, hit("10.0.0.1"))
	assert.Equal(t, http.StatusTooManyRequests, hit("10.0.0.1"))
	// Different IP gets its own bucket — still has budget.
	assert.Equal(t, http.StatusOK, hit("10.0.0.2"))
}

func TestRateLimit_StrictBucketIsIndependent(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		DefaultRate:  rate.Limit(100), // effectively unlimited
		DefaultBurst: 100,
		StrictRate:   rate.Limit(1),
		StrictBurst:  1,
	})
	r := gin.New()
	r.GET("/default", rl.Default(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.POST("/strict", rl.Strict(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	hit := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "10.0.0.1:1000"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	// /default stays open under its own burst.
	assert.Equal(t, http.StatusOK, hit(http.MethodGet, "/default"))
	// /strict: first pass.
	assert.Equal(t, http.StatusOK, hit(http.MethodPost, "/strict"))
	// /strict: second blocked — the strict bucket is not the same as default.
	assert.Equal(t, http.StatusTooManyRequests, hit(http.MethodPost, "/strict"))
	// /default still works — separate bucket.
	assert.Equal(t, http.StatusOK, hit(http.MethodGet, "/default"))
}
