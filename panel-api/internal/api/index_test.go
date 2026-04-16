package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
)

func TestServiceInfo_ReturnsJSON(t *testing.T) {
	t.Parallel()

	r := gin.New()
	api.RegisterServiceInfoRoute(r)

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "jabali-panel", body["service"])
	assert.Contains(t, body, "version")
	assert.Contains(t, body, "endpoints")
}

func TestNoMethod_ReturnsJSON405(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.HandleMethodNotAllowed = true
	api.RegisterServiceInfoRoute(r) // registers GET /info
	api.RegisterMethodNotAllowedHandler(r)

	req := httptest.NewRequest(http.MethodPost, "/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "method_not_allowed", body["error"])
}
