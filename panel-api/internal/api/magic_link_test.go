package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// magicLinkRouter wires a fresh gin engine for the M22 mint endpoint
// with optional Kratos claims injected. Returns the engine plus the
// mock agent + repos so tests can preload state and assert calls.
func magicLinkRouter(t *testing.T, userID string, isAdmin bool) (*gin.Engine, *mockWordPressInstallRepo, *mockDomainRepo, *mockUserRepo, *mockAgent) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	v1 := r.Group("/api/v1")
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{
				UserID:  userID,
				IsAdmin: isAdmin,
			})
			c.Next()
		})
	}

	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{}
	userRepo := &mockUserRepo{}
	agent := &mockAgent{}

	RegisterMagicLinkRoutes(v1, MagicLinkHandlerConfig{
		ApplicationInstalls: wpRepo,
		Domains:             domainRepo,
		Users:               userRepo,
		Agent:               agent,
	})

	return r, wpRepo, domainRepo, userRepo, agent
}

// fakeAgentResponse returns the JSON shape the agent's
// wordpress.create_sso_file emits.
func fakeAgentResponse(fileName string, expiresAt int64) json.RawMessage {
	body, _ := json.Marshal(map[string]any{
		"file_name":       fileName,
		"expires_at_unix": expiresAt,
	})
	return body
}

func seedReadyWPInstall(t *testing.T, wpRepo *mockWordPressInstallRepo, domainRepo *mockDomainRepo, userRepo *mockUserRepo, ownerUserID, installID, domainID, domainName, docRoot, subdirectory string) {
	t.Helper()
	uname := "shuki"
	userRepo.users = map[string]*models.User{
		ownerUserID: {ID: ownerUserID, Username: &uname},
	}
	domainRepo.domains = map[string]*models.Domain{
		domainID: {ID: domainID, Name: domainName, DocRoot: docRoot, UserID: ownerUserID},
	}
	wpRepo.installs = map[string]*models.WordPressInstall{
		installID: {
			ID:            installID,
			UserID:        ownerUserID,
			DomainID:      domainID,
			Status:        "ready",
			AppType:       "wordpress",
			AdminUsername: "admin",
			Subdirectory:  subdirectory,
		},
	}
}

func TestMagicLinkMint_HappyPath_RootInstall(t *testing.T) {
	const owner = "user_01ARZ3NDEKTSV4RRFFQ69G5OWN"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5IST"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5DOM"

	r, wpRepo, dRepo, uRepo, agent := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")

	const fakeFile = "jabali-sso-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.php"
	agent.callFn = func(ctx context.Context, command string, params any) (json.RawMessage, error) {
		if command != "wordpress.create_sso_file" {
			t.Errorf("agent.Call command = %q, want wordpress.create_sso_file", command)
		}
		p := params.(map[string]any)
		if p["install_path"] != "/home/shuki/domains/example.com/public_html" {
			t.Errorf("install_path = %v, want /home/shuki/domains/example.com/public_html", p["install_path"])
		}
		if p["os_user"] != "shuki" {
			t.Errorf("os_user = %v, want shuki", p["os_user"])
		}
		if p["install_id"] != installID {
			t.Errorf("install_id = %v, want %s", p["install_id"], installID)
		}
		if p["admin_username"] != "admin" {
			t.Errorf("admin_username = %v, want admin", p["admin_username"])
		}
		return fakeAgentResponse(fakeFile, 1700000000), nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var got mintResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantURL := "https://example.com/" + fakeFile
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
	if got.ExpiresIn != ssoTTLSeconds {
		t.Errorf("ExpiresIn = %d, want %d", got.ExpiresIn, ssoTTLSeconds)
	}
	if agent.callCount != 1 {
		t.Errorf("agent.Call invoked %d times, want 1", agent.callCount)
	}
}

func TestMagicLinkMint_HappyPath_SubdirectoryInstall(t *testing.T) {
	const owner = "user_X"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5SUB"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5SU2"

	r, wpRepo, dRepo, uRepo, agent := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "blog")

	const fakeFile = "jabali-sso-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB.php"
	agent.callFn = func(ctx context.Context, command string, params any) (json.RawMessage, error) {
		p := params.(map[string]any)
		if p["install_path"] != "/home/shuki/domains/example.com/public_html/blog" {
			t.Errorf("install_path = %v, want /home/shuki/domains/example.com/public_html/blog", p["install_path"])
		}
		return fakeAgentResponse(fakeFile, 1700000000), nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var got mintResponse
	json.Unmarshal(w.Body.Bytes(), &got)
	wantURL := "https://example.com/blog/" + fakeFile
	if got.URL != wantURL {
		t.Errorf("URL = %q, want %q", got.URL, wantURL)
	}
}

func TestMagicLinkMint_AuthMissing(t *testing.T) {
	r, _, _, _, _ := magicLinkRouter(t, "", false) // no claims injected

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/x/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestMagicLinkMint_CrossUserReturns404(t *testing.T) {
	const owner = "user_OWNER"
	const intruder = "user_INTRUDER"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5XU1"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5XU2"

	r, wpRepo, dRepo, uRepo, _ := magicLinkRouter(t, intruder, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (existence not leaked)", w.Code)
	}
}

func TestMagicLinkMint_InstallNotReady(t *testing.T) {
	const owner = "user_X"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5NRY"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5NR2"

	r, wpRepo, dRepo, uRepo, _ := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")
	wpRepo.installs[installID].Status = "installing"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "install_not_ready") {
		t.Errorf("body missing install_not_ready: %s", w.Body.String())
	}
}

func TestMagicLinkMint_NonWordPressAppType(t *testing.T) {
	const owner = "user_X"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5NWP"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5NW2"

	r, wpRepo, dRepo, uRepo, _ := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")
	wpRepo.installs[installID].AppType = "joomla"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unsupported_app_type") {
		t.Errorf("body missing unsupported_app_type: %s", w.Body.String())
	}
}

func TestMagicLinkMint_AgentError_Returns502(t *testing.T) {
	const owner = "user_X"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5AGE"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5AG2"

	r, wpRepo, dRepo, uRepo, agent := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")
	agent.callErr = errors.New("agent timeout")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "agent_failed") {
		t.Errorf("body missing agent_failed: %s", w.Body.String())
	}
}

func TestMagicLinkMint_AgentReturnsEmptyFileName_Returns502(t *testing.T) {
	const owner = "user_X"
	const installID = "01ARZ3NDEKTSV4RRFFQ69G5EMR"
	const domainID = "01ARZ3NDEKTSV4RRFFQ69G5EM2"

	r, wpRepo, dRepo, uRepo, agent := magicLinkRouter(t, owner, false)
	seedReadyWPInstall(t, wpRepo, dRepo, uRepo, owner, installID, domainID, "example.com", "/home/shuki/domains/example.com/public_html", "")
	agent.callFn = func(ctx context.Context, command string, params any) (json.RawMessage, error) {
		return json.RawMessage(`{"file_name":"","expires_at_unix":0}`), nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+installID+"/magic-link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

// composeInstallPath / composeSSOURL are pure functions; cover the
// edge cases the handler depends on.
func TestComposeInstallPath(t *testing.T) {
	cases := []struct {
		docRoot, subdir, want string
	}{
		{"/home/shuki/domains/x.com/public_html", "", "/home/shuki/domains/x.com/public_html"},
		{"/home/shuki/domains/x.com/public_html", "blog", "/home/shuki/domains/x.com/public_html/blog"},
		{"/home/shuki/domains/x.com/public_html", "/blog", "/home/shuki/domains/x.com/public_html/blog"},
		{"/home/shuki/domains/x.com/public_html", "blog/", "/home/shuki/domains/x.com/public_html/blog"},
		{"/home/shuki/domains/x.com/public_html/", "blog", "/home/shuki/domains/x.com/public_html/blog"},
		{"/home/shuki/domains/x.com/public_html", "wp/site", "/home/shuki/domains/x.com/public_html/wp/site"},
	}
	for _, c := range cases {
		got := composeInstallPath(c.docRoot, c.subdir)
		if got != c.want {
			t.Errorf("composeInstallPath(%q, %q) = %q, want %q", c.docRoot, c.subdir, got, c.want)
		}
	}
}

func TestComposeSSOURL(t *testing.T) {
	const f = "jabali-sso-XYZ.php"
	cases := []struct {
		domain, subdir, want string
	}{
		{"example.com", "", "https://example.com/" + f},
		{"example.com", "blog", "https://example.com/blog/" + f},
		{"example.com", "/blog/", "https://example.com/blog/" + f},
	}
	for _, c := range cases {
		got := composeSSOURL(c.domain, c.subdir, f)
		if got != c.want {
			t.Errorf("composeSSOURL(%q, %q, %q) = %q, want %q", c.domain, c.subdir, f, got, c.want)
		}
	}
}
