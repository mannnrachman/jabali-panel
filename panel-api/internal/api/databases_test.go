package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Mock implementations

type mockDatabaseRepo struct {
	databases []models.Database
	findErr   error
	listErr   error
}

func (m *mockDatabaseRepo) FindByID(ctx context.Context, id string) (*models.Database, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	for _, db := range m.databases {
		if db.ID == id {
			return &db, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockDatabaseRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.Database, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	return m.databases, int64(len(m.databases)), nil
}

func (m *mockDatabaseRepo) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.Database, int64, error) {
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	var filtered []models.Database
	for _, db := range m.databases {
		if db.UserID == userID {
			filtered = append(filtered, db)
		}
	}
	return filtered, int64(len(filtered)), nil
}

func (m *mockDatabaseRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	for _, db := range m.databases {
		if db.UserID == userID {
			count++
		}
	}
	return count, nil
}

type mockDatabaseUserRepo struct{}

func (m *mockDatabaseUserRepo) FindByID(ctx context.Context, id string) (*models.DatabaseUser, error) {
	return nil, nil
}

func (m *mockDatabaseUserRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.DatabaseUser, int64, error) {
	return nil, 0, nil
}

func (m *mockDatabaseUserRepo) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.DatabaseUser, int64, error) {
	return nil, 0, nil
}

func (m *mockDatabaseUserRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	return 0, nil
}

type mockUserRepo struct{}

func (m *mockUserRepo) Create(ctx context.Context, u *models.User) error {
	return nil
}

func (m *mockUserRepo) FindByID(ctx context.Context, id string) (*models.User, error) {
	return nil, nil
}

func (m *mockUserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	return nil, nil
}

func (m *mockUserRepo) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	return nil, nil
}

func (m *mockUserRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.User, int64, error) {
	return nil, 0, nil
}

func (m *mockUserRepo) Update(ctx context.Context, u *models.User) error {
	return nil
}

func (m *mockUserRepo) SetAdmin(ctx context.Context, id string, isAdmin bool) error {
	return nil
}

func (m *mockUserRepo) CountAdmins(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *mockUserRepo) FindAdminsByEmail(ctx context.Context) ([]*models.User, error) {
	return nil, nil
}

func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	return nil
}

type mockPackageRepo struct{}

func (m *mockPackageRepo) Create(ctx context.Context, p *models.HostingPackage) error {
	return nil
}

func (m *mockPackageRepo) FindByID(ctx context.Context, id string) (*models.HostingPackage, error) {
	return nil, nil
}

func (m *mockPackageRepo) FindByName(ctx context.Context, name string) (*models.HostingPackage, error) {
	return nil, nil
}

func (m *mockPackageRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.HostingPackage, int64, error) {
	return nil, 0, nil
}

func (m *mockPackageRepo) Update(ctx context.Context, p *models.HostingPackage) error {
	return nil
}

func (m *mockPackageRepo) Delete(ctx context.Context, id string) error {
	return nil
}

// Helper to setup router with optional claims
func databaseRouter(userID string, isAdmin bool) (*gin.Engine, *mockDatabaseRepo) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	// Inject claims if provided
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{
				UserID:  userID,
				IsAdmin: isAdmin,
			})
			c.Next()
		})
	}

	dbRepo := &mockDatabaseRepo{}

	RegisterDatabaseRoutes(v1, DatabaseHandlerConfig{
		Databases:      dbRepo,
		DatabaseUsers:  &mockDatabaseUserRepo{},
		Users:          &mockUserRepo{},
		Packages:       &mockPackageRepo{},
	})

	return r, dbRepo
}

// Test cases

func TestDatabaseListUnauthenticated(t *testing.T) {
	r, _ := databaseRouter("", false)

	req := httptest.NewRequest("GET", "/api/v1/databases", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestDatabaseListAdmin(t *testing.T) {
	r, dbRepo := databaseRouter("admin1", true)

	// Setup test data
	db1 := models.Database{ID: "id1", UserID: "user1", Name: "db1"}
	db2 := models.Database{ID: "id2", UserID: "user2", Name: "db2"}
	dbRepo.databases = []models.Database{db1, db2}

	req := httptest.NewRequest("GET", "/api/v1/databases", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, float64(2), resp["total"])
	assert.NotNil(t, resp["items"])
}

func TestDatabaseListUser(t *testing.T) {
	r, dbRepo := databaseRouter("user1", false)

	// Setup test data
	db1 := models.Database{ID: "id1", UserID: "user1", Name: "db1"}
	db2 := models.Database{ID: "id2", UserID: "user2", Name: "db2"}
	dbRepo.databases = []models.Database{db1, db2}

	req := httptest.NewRequest("GET", "/api/v1/databases", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	// User should only see their own database
	assert.Equal(t, float64(1), resp["total"])
}

func TestDatabaseListError(t *testing.T) {
	r, dbRepo := databaseRouter("admin1", true)
	dbRepo.listErr = errors.New("database error")

	req := httptest.NewRequest("GET", "/api/v1/databases", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "internal", resp["error"])
}

func TestDatabaseGetUnauthenticated(t *testing.T) {
	r, _ := databaseRouter("", false)

	req := httptest.NewRequest("GET", "/api/v1/databases/id1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestDatabaseGetAdmin(t *testing.T) {
	r, dbRepo := databaseRouter("admin1", true)

	// Setup test data
	db1 := models.Database{ID: "id1", UserID: "user1", Name: "db1"}
	dbRepo.databases = []models.Database{db1}

	req := httptest.NewRequest("GET", "/api/v1/databases/id1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "id1", resp["id"])
}

func TestDatabaseGetUserOwned(t *testing.T) {
	r, dbRepo := databaseRouter("user1", false)

	// Setup test data
	db1 := models.Database{ID: "id1", UserID: "user1", Name: "db1"}
	dbRepo.databases = []models.Database{db1}

	req := httptest.NewRequest("GET", "/api/v1/databases/id1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "id1", resp["id"])
}

func TestDatabaseGetUserForbidden(t *testing.T) {
	r, dbRepo := databaseRouter("user1", false)

	// Setup test data - database belongs to different user
	db1 := models.Database{ID: "id1", UserID: "user2", Name: "db1"}
	dbRepo.databases = []models.Database{db1}

	req := httptest.NewRequest("GET", "/api/v1/databases/id1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "forbidden", resp["error"])
}

func TestDatabaseGetNotFound(t *testing.T) {
	r, dbRepo := databaseRouter("admin1", true)
	dbRepo.databases = []models.Database{}

	req := httptest.NewRequest("GET", "/api/v1/databases/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "not_found", resp["error"])
}

func TestDatabaseGetError(t *testing.T) {
	r, dbRepo := databaseRouter("admin1", true)
	dbRepo.findErr = errors.New("database error")

	req := httptest.NewRequest("GET", "/api/v1/databases/id1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "internal", resp["error"])
}
