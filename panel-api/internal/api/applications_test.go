package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// applicationsRouter wires the M19 generic /applications surface for
// tests. Mirrors wordPressRouter but mounts RegisterApplicationRoutes
// + a populated registry. Returns the agent mock so individual tests
// can assert on the dispatched commands.
func applicationsRouter(t *testing.T, userID string, isAdmin bool, wpRepo *mockWordPressInstallRepo, domainRepo *mockDomainRepo, userRepo *mockUserRepo, registerExtras func(*apps.Registry)) (*gin.Engine, *mockAgent, *mockDatabaseRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{UserID: userID, IsAdmin: isAdmin})
			c.Next()
		})
	}

	registry := apps.New()
	if err := apps.RegisterDefaults(registry); err != nil {
		t.Fatalf("RegisterDefaults: %v", err)
	}
	if registerExtras != nil {
		registerExtras(registry)
	}

	dbRepo := &mockDatabaseRepo{}
	ag := &mockAgent{}

	cfg := ApplicationHandlerConfig{
		ApplicationInstalls: wpRepo,
		Databases:           dbRepo,
		DatabaseUsers:       &mockDatabaseUserRepo{},
		DatabaseGrants:      &mockDatabaseGrantRepo{},
		Domains:             domainRepo,
		Users:               userRepo,
		Packages:            &mockPackageRepo{},
		Agent:               ag,
		Apps:                registry,
	}
	RegisterApplicationRoutes(v1, cfg)
	return r, ag, dbRepo
}

func wpUserAndDomain() (*mockWordPressInstallRepo, *mockDomainRepo, *mockUserRepo) {
	return &mockWordPressInstallRepo{},
		&mockDomainRepo{
			domains: map[string]*models.Domain{
				"domain1": {ID: "domain1", UserID: "user1", Name: "example.com", DocRoot: "/home/alice/domains/example.com/public_html"},
			},
		},
		&mockUserRepo{
			users: map[string]*models.User{
				"user1": {ID: "user1", Username: strPtr("alice")},
			},
		}
}

func TestApplications_GetRegistry_IncludesWordPress(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	req := httptest.NewRequest("GET", "/api/v1/applications/registry", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var resp struct {
		Data []registryEntry `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range resp.Data {
		if e.Name == "wordpress" {
			found = true
			if !e.RequiresDB {
				t.Error("wordpress entry should advertise requires_db=true")
			}
			if e.DisplayName == "" {
				t.Error("wordpress entry should have a display name")
			}
			break
		}
	}
	if !found {
		t.Errorf("registry missing wordpress entry: %+v", resp.Data)
	}
}

func TestApplications_CreateWordPress_HappyPath(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, ag, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":     "wordpress",
		"domain_id":    "domain1",
		"subdirectory": "blog",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "s3cret-pw",
			"site_title":     "Hello",
			"locale":         "en_US",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	var resp createApplicationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AppType != "wordpress" {
		t.Errorf("AppType = %q", resp.AppType)
	}
	if resp.Status != "pending" {
		t.Errorf("Status = %q", resp.Status)
	}
	if resp.DBID == "" {
		t.Error("WordPress install should provision a DB and return its id")
	}
	if resp.AdminPassword != "s3cret-pw" {
		t.Errorf("AdminPassword should echo the supplied value, got %q", resp.AdminPassword)
	}
	if resp.AdminUsername == "" {
		t.Error("AdminUsername should be auto-generated server-side, got empty")
	}
	// We can't read ag.callCount here without racing the install kicker
	// goroutine — DBID being non-empty already proves the synchronous
	// db.create / db_user.create / db_user.grant chain ran. The kicker
	// itself is exercised by wordpress_test.go in the legacy WP suite.
	_ = ag
}

func TestApplications_CreateRequiresDBFalse_SkipsDBChain(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, ag, dbRepo := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, func(reg *apps.Registry) {
		// Register a flat-file app so the RequiresDB=false branch runs.
		// Step 6 will replace this with the real DokuWiki descriptor;
		// here we only need to prove the framework skips DB provisioning.
		_ = reg.Register(apps.App{
			Name:        "flatfile",
			DisplayName: "Flatfile",
			RequiresDB:  false,
			InstallParamSchema: map[string]apps.ParamSpec{
				"admin_email":    {Type: "email", Required: true},
				"admin_password": {Type: "password", Required: true},
			},
		})
	})

	body := map[string]any{
		"app_type":  "flatfile",
		"domain_id": "domain1",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "s3cret-pw",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	var resp createApplicationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.DBID != "" {
		t.Errorf("RequiresDB=false: expected empty DBID, got %q", resp.DBID)
	}
	if ag.callCount != 0 {
		t.Errorf("RequiresDB=false: expected 0 agent calls (no DB chain, no install kicker for non-WP), got %d", ag.callCount)
	}
	if len(dbRepo.databases) != 0 {
		t.Errorf("RequiresDB=false: expected 0 db rows created, got %d", len(dbRepo.databases))
	}
}

func TestApplications_Create_InvalidAppType_400(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":  "magento",
		"domain_id": "domain1",
		"params":    map[string]any{},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_app_type") {
		t.Errorf("body missing invalid_app_type: %s", w.Body.String())
	}
}

func TestApplications_Create_MissingRequiredParam_400(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":  "wordpress",
		"domain_id": "domain1",
		"params": map[string]any{
			// site_title missing — required by descriptor.
			// admin_username is no longer in the schema — it's auto-
			// generated server-side, so we exercise a different
			// required field here.
			"admin_email":    "admin@example.com",
			"admin_password": "p",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_params") {
		t.Errorf("body missing invalid_params: %s", w.Body.String())
	}
}

func TestApplications_Create_BadEmailParam_400(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":  "wordpress",
		"domain_id": "domain1",
		"params": map[string]any{
			"admin_email":    "not-an-email",
			"admin_password": "p",
			"site_title":     "t",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_params") {
		t.Errorf("body missing invalid_params: %s", w.Body.String())
	}
}

func TestApplications_Create_UnknownParam_400(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":  "wordpress",
		"domain_id": "domain1",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "p",
			"site_title":     "t",
			"surprise":       "extra",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
}

func TestApplications_Create_DuplicateAppTypeAtSameSlot_409(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	wpRepo.installs = map[string]*models.WordPressInstall{
		"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Subdirectory: "blog", AppType: "wordpress"},
	}
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":     "wordpress",
		"domain_id":    "domain1",
		"subdirectory": "blog",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "p",
			"site_title":     "t",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "install_exists") {
		t.Errorf("body missing install_exists: %s", w.Body.String())
	}
}

func TestApplications_Create_DifferentAppTypeSameSlot_Conflict(t *testing.T) {
	// Per the operator's directive (2026-04-19): a (domain, subdir) slot
	// hosts AT MOST ONE application — even if the second install is a
	// different app_type. This is stricter than the DB-level UNIQUE in
	// migration 000046 (which still allows distinct app_types in the
	// same slot for forward compat); the API check enforces the
	// product rule. ADR-0033 was updated to reflect this.
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	wpRepo.installs = map[string]*models.WordPressInstall{
		"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Subdirectory: "wiki", AppType: "wordpress"},
	}
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, func(reg *apps.Registry) {
		_ = reg.Register(apps.App{
			Name:        "flatfile",
			DisplayName: "Flatfile",
			RequiresDB:  false,
			InstallParamSchema: map[string]apps.ParamSpec{
				"admin_email":    {Type: "email", Required: true},
				"admin_password": {Type: "password", Required: true},
			},
		})
	})

	body := map[string]any{
		"app_type":     "flatfile",
		"domain_id":    "domain1",
		"subdirectory": "wiki",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "p",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 (slot already taken regardless of app_type): %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "install_exists") {
		t.Errorf("body missing install_exists: %s", w.Body.String())
	}
}

func TestApplications_Create_DomainNotOwned_404(t *testing.T) {
	wpRepo, _, userRepo := wpUserAndDomain()
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domainX": {ID: "domainX", UserID: "userOTHER", Name: "other.com"},
		},
	}
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":  "wordpress",
		"domain_id": "domainX",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "p",
			"site_title":     "t",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
}

func TestApplications_Create_BadSubdirectory_400(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	body := map[string]any{
		"app_type":     "wordpress",
		"domain_id":    "domain1",
		"subdirectory": "../escape",
		"params": map[string]any{
			"admin_email":    "admin@example.com",
			"admin_password": "p",
			"site_title":     "t",
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/applications", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_subdirectory") {
		t.Errorf("body missing invalid_subdirectory: %s", w.Body.String())
	}
}

func TestApplications_List_DelegatesToWPHandler(t *testing.T) {
	wpRepo, domainRepo, userRepo := wpUserAndDomain()
	wpRepo.installs = map[string]*models.WordPressInstall{
		"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", AppType: "wordpress", Status: "ready"},
	}
	r, _, _ := applicationsRouter(t, "user1", false, wpRepo, domainRepo, userRepo, nil)

	req := httptest.NewRequest("GET", "/api/v1/applications", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"] != float64(1) {
		t.Fatalf("total = %v", resp["total"])
	}
}

func TestValidateInstallParams_TableDriven(t *testing.T) {
	pat := "^[a-z]+$"
	schema := map[string]apps.ParamSpec{
		"name":   {Type: "string", Required: true, Pattern: &pat},
		"email":  {Type: "email", Required: false},
		"flag":   {Type: "bool", Required: false},
		"choice": {Type: "enum", Values: []string{"a", "b"}, Required: true},
	}
	cases := []struct {
		name    string
		params  map[string]any
		wantErr string
	}{
		{"happy", map[string]any{"name": "abc", "choice": "a"}, ""},
		{"missing required", map[string]any{"choice": "a"}, "required"},
		{"pattern fail", map[string]any{"name": "ABC", "choice": "a"}, "pattern"},
		{"enum fail", map[string]any{"name": "abc", "choice": "z"}, "must be one of"},
		{"wrong type bool", map[string]any{"name": "abc", "choice": "a", "flag": "yes"}, "expected bool"},
		{"unknown field", map[string]any{"name": "abc", "choice": "a", "extra": 1}, "unknown param"},
		{"bad email", map[string]any{"name": "abc", "choice": "a", "email": "no-at-sign"}, "invalid email"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateInstallParams(c.params, schema)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %q", c.wantErr, err.Error())
			}
		})
	}
}

// Compile-time guard: the legacy alias keeps existing wiring valid.
var _ WordPressHandlerConfig = ApplicationHandlerConfig{}

// Use context to keep imports tight in case future tests need it.
var _ = context.Background
