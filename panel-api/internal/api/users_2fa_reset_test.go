package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

// ptr returns a pointer to s — tiny helper for seeding KratosIdentityID.
func ptr(s string) *string { return &s }

// fake2FAServer builds a test Kratos-admin stub that responds to
// PATCH /admin/identities/<id> with the given status for every request.
func fake2FAServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
	}))
}

// fake2FAServerBody returns a stub whose response body contains the given JSON.
func fake2FAServerBody(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestUsers_Reset2FA_Success(t *testing.T) {
	t.Parallel()

	srv := fake2FAServer(http.StatusOK)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	target := makeUser(t, "user@example.com", false, "userpassword")
	target.KratosIdentityID = ptr("kratos-id-abc")
	repo.seed(target)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
}

func TestUsers_Reset2FA_UserNotFound(t *testing.T) {
	t.Parallel()

	srv := fake2FAServer(http.StatusOK)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/nonexistent-id/2fa/reset", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "user not found", resp["error"])
}

func TestUsers_Reset2FA_NoKratosIdentity(t *testing.T) {
	t.Parallel()

	srv := fake2FAServer(http.StatusOK)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	// User exists in panel DB but has no Kratos identity (pre-migration user).
	target := makeUser(t, "legacy@example.com", false, "legacypassword")
	// KratosIdentityID is nil by default from makeUser.
	repo.seed(target)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "user_has_no_kratos_identity", resp["error"])
}

func TestUsers_Reset2FA_NoKratosClient(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	target := makeUser(t, "user@example.com", false, "userpassword")
	target.KratosIdentityID = ptr("kratos-id-xyz")
	repo.seed(target)

	// Empty kratosURL → KratosClient is nil.
	r := buildRouterWithKratos(repo, "", &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "kratos not available", resp["error"])
}

func TestUsers_Reset2FA_KratosIdentityNotFound(t *testing.T) {
	t.Parallel()

	// Kratos returns 404 — the identity was deleted on the Kratos side without
	// the panel knowing. RemoveSecondFactor maps this to ErrIdentityNotFound.
	srv := fake2FAServerBody(http.StatusNotFound, `{"error":{"id":"404","message":"not found"}}`)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	target := makeUser(t, "user@example.com", false, "userpassword")
	target.KratosIdentityID = ptr("kratos-id-gone")
	repo.seed(target)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "kratos identity not found", resp["error"])
}

func TestUsers_Reset2FA_KratosServerError(t *testing.T) {
	t.Parallel()

	// Kratos returns 500 — transient error, operator should retry.
	srv := fake2FAServer(http.StatusInternalServerError)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	target := makeUser(t, "user@example.com", false, "userpassword")
	target.KratosIdentityID = ptr("kratos-id-fail")
	repo.seed(target)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "kratos admin patch failed", resp["error"])
}

func TestUsers_Reset2FA_NoPreviousTotp_StillSucceeds(t *testing.T) {
	t.Parallel()

	// Kratos returns 422 for both removes (paths absent) — this is the
	// "user never enrolled 2FA" case. RemoveSecondFactor treats 422 as
	// success. Handler should return 200.
	srv := fake2FAServer(http.StatusUnprocessableEntity)
	defer srv.Close()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	target := makeUser(t, "user@example.com", false, "userpassword")
	target.KratosIdentityID = ptr("kratos-id-no2fa")
	repo.seed(target)

	r := buildRouterWithKratos(repo, srv.URL, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/admin/users/"+target.ID+"/2fa/reset", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
}
