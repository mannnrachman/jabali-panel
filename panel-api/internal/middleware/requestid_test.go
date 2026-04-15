package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

var ulidRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func init() { gin.SetMode(gin.TestMode) }

func TestRequestID_MintsWhenMissing(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, ginctx.RequestID(c)) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, ulidRE.MatchString(rec.Body.String()), "minted id must be a ULID: %q", rec.Body.String())
	assert.Equal(t, rec.Body.String(), rec.Header().Get("X-Request-ID"),
		"response header and context must agree")
}

func TestRequestID_HonoursIncomingHeader(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.Use(middleware.RequestID())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, ginctx.RequestID(c)) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "caller-supplied-123")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "caller-supplied-123", rec.Body.String())
	assert.Equal(t, "caller-supplied-123", rec.Header().Get("X-Request-ID"))
}

func TestRequestID_RejectsBogusHeader(t *testing.T) {
	t.Parallel()

	// Callers that inject junk (control chars, newlines, excessively long)
	// get their value dropped and a freshly minted ULID instead.
	cases := []string{
		"line1\nline2",                           // newline injection
		"\tvalue",                                // control char
		"",                                       // empty allowed (same as missing)
		"1234567890" + string(make([]byte, 512)), // too long
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.RequestID())
			r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, ginctx.RequestID(c)) })

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-Request-ID", in)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.True(t, ulidRE.MatchString(rec.Body.String()),
				"bogus header %q should have been replaced with a fresh ULID; got %q", in, rec.Body.String())
		})
	}
}
