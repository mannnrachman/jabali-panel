package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ---------- in-memory repo ----------

// memUserRepo is a tiny in-memory UserRepository for handler tests.
type memUserRepo struct {
	mu   sync.Mutex
	byID map[string]*models.User
}

func newMemUserRepo() *memUserRepo {
	return &memUserRepo{byID: map[string]*models.User{}}
}

func (m *memUserRepo) seed(u *models.User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *u
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
		c.UpdatedAt = c.CreatedAt
	}
	m.byID[c.ID] = &c
}

func (m *memUserRepo) Create(_ context.Context, u *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.byID {
		if v.Email == u.Email {
			return repository.ErrConflict
		}
	}
	c := *u
	c.CreatedAt = time.Now()
	c.UpdatedAt = c.CreatedAt
	m.byID[c.ID] = &c
	*u = c
	return nil
}

func (m *memUserRepo) FindByID(_ context.Context, id string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.byID[id]; ok {
		c := *u
		return &c, nil
	}
	return nil, repository.ErrNotFound
}

func (m *memUserRepo) FindByEmail(_ context.Context, email string) (*models.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.byID {
		if v.Email == email {
			c := *v
			return &c, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *memUserRepo) List(_ context.Context, offset, limit int) ([]models.User, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := make([]models.User, 0, len(m.byID))
	for _, v := range m.byID {
		all = append(all, *v)
	}
	total := int64(len(all))
	if offset > len(all) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

func (m *memUserRepo) Update(_ context.Context, u *models.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.byID[u.ID]
	if !ok {
		return repository.ErrNotFound
	}
	// mimic the real repo: leave is_admin alone
	existing.Email = u.Email
	existing.NameFirst = u.NameFirst
	existing.NameLast = u.NameLast
	existing.PasswordHash = u.PasswordHash
	existing.UpdatedAt = time.Now()
	return nil
}

func (m *memUserRepo) SetAdmin(_ context.Context, id string, isAdmin bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return repository.ErrNotFound
	}
	u.IsAdmin = isAdmin
	u.UpdatedAt = time.Now()
	return nil
}

func (m *memUserRepo) CountAdmins(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, v := range m.byID {
		if v.IsAdmin {
			n++
		}
	}
	return n, nil
}

func (m *memUserRepo) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.byID, id)
	return nil
}

// ---------- test harness ----------

// buildRouter wires a minimal Gin engine with users routes mounted behind a
// fake-auth middleware that stamps the given claims onto the context.
func buildRouter(repo repository.UserRepository, claims *auth.AccessClaims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1", func(c *gin.Context) {
		if claims != nil {
			ginctx.SetClaims(c, claims)
		}
		c.Next()
	})
	api.RegisterUserRoutes(g, api.UserHandlerConfig{
		Repo:       repo,
		BcryptCost: bcrypt.MinCost,
	})
	return r
}

func makeUser(t *testing.T, email string, isAdmin bool, password string) *models.User {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return &models.User{
		ID:           ids.NewULID(),
		Email:        email,
		PasswordHash: string(h),
		IsAdmin:      isAdmin,
	}
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// ---------- LIST ----------

func TestUsers_List_AdminSeesAll(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)
	repo.seed(makeUser(t, "u1@example.com", false, "password01"))
	repo.seed(makeUser(t, "u2@example.com", false, "password02"))

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, Email: admin.Email, IsAdmin: true})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data     []models.User `json:"data"`
		Total    int64         `json:"total"`
		Page     int           `json:"page"`
		PageSize int           `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(3), resp.Total)
	assert.Len(t, resp.Data, 3)
	assert.Equal(t, 1, resp.Page)
}

func TestUsers_List_NonAdmin403(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	user := makeUser(t, "u@example.com", false, "password01")
	repo.seed(user)

	r := buildRouter(repo, &auth.AccessClaims{UserID: user.ID, Email: user.Email})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUsers_List_PaginationClamps(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users?page=-5&page_size=0", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["page"])
	assert.Equal(t, float64(20), resp["page_size"]) // defaultUsersPageSize
}

// ---------- GET ----------

func TestUsers_Get_OwnerAllowed(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	u := makeUser(t, "owner@example.com", false, "password01")
	repo.seed(u)

	r := buildRouter(repo, &auth.AccessClaims{UserID: u.ID, Email: u.Email})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users/"+u.ID, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUsers_Get_StrangerForbidden(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	a := makeUser(t, "a@example.com", false, "password01")
	b := makeUser(t, "b@example.com", false, "password02")
	repo.seed(a)
	repo.seed(b)

	// A tries to fetch B.
	r := buildRouter(repo, &auth.AccessClaims{UserID: a.ID})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users/"+b.ID, nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUsers_Get_PasswordHashNotLeaked(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	u := makeUser(t, "u@example.com", false, "password01")
	repo.seed(u)

	r := buildRouter(repo, &auth.AccessClaims{UserID: u.ID})
	rec := doJSON(t, r, http.MethodGet, "/api/v1/users/"+u.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "password_hash")
	assert.NotContains(t, rec.Body.String(), u.PasswordHash)
}

// ---------- CREATE ----------

func TestUsers_Create_Admin(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/users", map[string]any{
		"email":    "new@example.com",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var out models.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "new@example.com", out.Email)
	assert.NotEmpty(t, out.ID)
	assert.False(t, out.IsAdmin)
}

func TestUsers_Create_ShortPassword400(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/users", map[string]any{
		"email":    "x@example.com",
		"password": "short",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUsers_Create_DuplicateEmail409(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	existing := makeUser(t, "dup@example.com", false, "password01")
	repo.seed(admin)
	repo.seed(existing)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/users", map[string]any{
		"email":    "dup@example.com",
		"password": "password123",
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestUsers_Create_NonAdmin403(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	user := makeUser(t, "u@example.com", false, "password01")
	repo.seed(user)

	r := buildRouter(repo, &auth.AccessClaims{UserID: user.ID})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/users", map[string]any{
		"email":    "x@example.com",
		"password": "password123",
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------- PATCH ----------

func TestUsers_Patch_OwnerChangesOwnPassword(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	u := makeUser(t, "u@example.com", false, "password01")
	repo.seed(u)

	r := buildRouter(repo, &auth.AccessClaims{UserID: u.ID})
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/users/"+u.ID, map[string]any{
		"password":         "newpassword9",
		"current_password": "password01",
	})
	require.Equal(t, http.StatusOK, rec.Code)

	// verify the hash changed
	after, err := repo.FindByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.NotEqual(t, u.PasswordHash, after.PasswordHash)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("newpassword9")))
}

func TestUsers_Patch_OwnerWrongCurrentPassword401(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	u := makeUser(t, "u@example.com", false, "password01")
	repo.seed(u)

	r := buildRouter(repo, &auth.AccessClaims{UserID: u.ID})
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/users/"+u.ID, map[string]any{
		"password":         "newpassword9",
		"current_password": "not-the-password",
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUsers_Patch_OwnerCannotSetIsAdmin(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	u := makeUser(t, "u@example.com", false, "password01")
	repo.seed(u)

	r := buildRouter(repo, &auth.AccessClaims{UserID: u.ID})
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/users/"+u.ID, map[string]any{
		"is_admin": true,
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUsers_Patch_AdminFlipsIsAdmin(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	admin2 := makeUser(t, "admin2@example.com", true, "adminpassword") // keep >1 admin
	target := makeUser(t, "u@example.com", false, "password01")
	repo.seed(admin)
	repo.seed(admin2)
	repo.seed(target)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/users/"+target.ID, map[string]any{
		"is_admin": true,
	})
	require.Equal(t, http.StatusOK, rec.Code)

	after, _ := repo.FindByID(context.Background(), target.ID)
	assert.True(t, after.IsAdmin)
}

func TestUsers_Patch_CannotDemoteLastAdmin(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodPatch, "/api/v1/users/"+admin.ID, map[string]any{
		"is_admin": false,
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot_demote_last_admin")
}

// ---------- DELETE ----------

func TestUsers_Delete_Admin(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	target := makeUser(t, "victim@example.com", false, "password01")
	repo.seed(admin)
	repo.seed(target)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodDelete, "/api/v1/users/"+target.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	_, err := repo.FindByID(context.Background(), target.ID)
	assert.True(t, errors.Is(err, repository.ErrNotFound))
}

func TestUsers_Delete_CannotDeleteSelf(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	repo.seed(admin)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodDelete, "/api/v1/users/"+admin.ID, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot_delete_self")
}

func TestUsers_Delete_SoleAdminCanDeleteNonAdmin(t *testing.T) {
	t.Parallel()

	// Confirms the last-admin check doesn't fire when the target isn't an
	// admin. (The check can only bite when an admin caller deletes a
	// different admin and that target happens to be the last admin —
	// unreachable in practice because there'd then be ≥2 admins, but we
	// keep the guard as defence-in-depth.)
	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	victim := makeUser(t, "v@example.com", false, "password01")
	repo.seed(admin)
	repo.seed(victim)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodDelete, "/api/v1/users/"+victim.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ---------- smoke: bytes-free body on DELETE & Content-Length header ----------

func TestUsers_Delete_HasNoContentLength(t *testing.T) {
	t.Parallel()

	repo := newMemUserRepo()
	admin := makeUser(t, "admin@example.com", true, "adminpassword")
	target := makeUser(t, "v@example.com", false, "password01")
	repo.seed(admin)
	repo.seed(target)

	r := buildRouter(repo, &auth.AccessClaims{UserID: admin.ID, IsAdmin: true})
	rec := doJSON(t, r, http.MethodDelete, "/api/v1/users/"+target.ID, nil)

	require.Equal(t, http.StatusNoContent, rec.Code)
	cl := rec.Header().Get("Content-Length")
	if cl != "" {
		n, err := strconv.Atoi(cl)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	}
	// body must be empty or whitespace — defensive
	assert.LessOrEqual(t, len(bytes.TrimSpace(rec.Body.Bytes())), 0)
}
