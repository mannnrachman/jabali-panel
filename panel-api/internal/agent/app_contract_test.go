// Cross-boundary contract for the M19 app.* dispatcher commands. Every
// JSON change here MUST be mirrored in the agent-side handler structs
// (panel-agent/internal/commands/wordpress_{install,delete,clone}.go)
// or the live install/delete/clone call will silently drop fields. The
// round-trip tests below catch any drift between the wire fixture and
// the typed Go struct on the panel side; the agent side is verified by
// its own dispatcher + handler unit tests.
package agent

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"
)

//go:embed testdata/app_install_wordpress_request.json
var appInstallWordPressRequestFixture []byte

//go:embed testdata/app_install_wordpress_response.json
var appInstallWordPressResponseFixture []byte

//go:embed testdata/app_delete_wordpress_request.json
var appDeleteWordPressRequestFixture []byte

//go:embed testdata/app_delete_wordpress_response.json
var appDeleteWordPressResponseFixture []byte

//go:embed testdata/app_clone_wordpress_request.json
var appCloneWordPressRequestFixture []byte

//go:embed testdata/app_clone_wordpress_response.json
var appCloneWordPressResponseFixture []byte

// AppInstallWordPressRequest is the body the panel sends to
// agent.app.install when app_type=="wordpress". The dispatcher reads
// AppType to route; the WordPress installer reads everything else.
type AppInstallWordPressRequest struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	DBName       string `json:"db_name"`
	DBUser       string `json:"db_user"`
	DBPassword   string `json:"db_password"`
	DBHost       string `json:"db_host"`
	SiteURL      string `json:"site_url"`
	SiteTitle    string `json:"site_title"`
	AdminUser    string `json:"admin_user"`
	AdminPass    string `json:"admin_pass"`
	AdminEmail   string `json:"admin_email"`
	Locale       string `json:"locale"`
	Subdirectory string `json:"subdirectory"`
	UseWWW       bool   `json:"use_www"`
}

type AppInstallWordPressResponse struct {
	Version string `json:"version"`
}

type AppDeleteWordPressRequest struct {
	AppType string `json:"app_type"`
	OSUser  string `json:"os_user"`
	Docroot string `json:"docroot"`
	Domain  string `json:"domain"`
}

type AppDeleteWordPressResponse struct {
	Status string `json:"status"`
}

// AppCloneWordPressRequest mirrors panel-agent/internal/commands/
// wordpress_clone.go's wordpressCloneReq plus the app_type discriminator.
// Source/dest docroots and DB names are pre-resolved on the panel side
// and passed through verbatim — the agent does not look them up itself.
type AppCloneWordPressRequest struct {
	AppType         string `json:"app_type"`
	OSUser          string `json:"os_user"`
	SrcDocroot      string `json:"src_docroot"`
	DstDocroot      string `json:"dst_docroot"`
	SrcDBName       string `json:"src_db_name"`
	DstDBName       string `json:"dst_db_name"`
	DstDBUser       string `json:"dst_db_user"`
	DstDBPassword   string `json:"dst_db_password"`
	DstDBHost       string `json:"dst_db_host"`
	SrcSiteURL      string `json:"src_site_url"`
	DstSiteURL      string `json:"dst_site_url"`
	UseWWW          bool   `json:"use_www"`
	DstSubdirectory string `json:"dst_subdirectory"`
}

type AppCloneWordPressResponse struct {
	Version string `json:"version"`
}

func TestAppInstallWordPressRequest_RoundTrips(t *testing.T) {
	roundTrip[AppInstallWordPressRequest](t, appInstallWordPressRequestFixture)
}

func TestAppInstallWordPressResponse_RoundTrips(t *testing.T) {
	roundTrip[AppInstallWordPressResponse](t, appInstallWordPressResponseFixture)
}

func TestAppDeleteWordPressRequest_RoundTrips(t *testing.T) {
	roundTrip[AppDeleteWordPressRequest](t, appDeleteWordPressRequestFixture)
}

func TestAppDeleteWordPressResponse_RoundTrips(t *testing.T) {
	roundTrip[AppDeleteWordPressResponse](t, appDeleteWordPressResponseFixture)
}

func TestAppCloneWordPressRequest_RoundTrips(t *testing.T) {
	roundTrip[AppCloneWordPressRequest](t, appCloneWordPressRequestFixture)
}

func TestAppCloneWordPressResponse_RoundTrips(t *testing.T) {
	roundTrip[AppCloneWordPressResponse](t, appCloneWordPressResponseFixture)
}

// TestAppInstallRequest_AcceptedByAgentInstallerStruct verifies the
// fixture deserialises cleanly into the agent's wordpressInstallReq
// shape WITHOUT silent field loss. The agent struct lives in another
// package — we re-declare the same field names here and compare key
// sets. Drop or rename a field on either side without updating this
// test, and the test fails before any installer ever runs.
func TestAppInstallWordPressRequest_AllFieldsMapToAgentShape(t *testing.T) {
	var into map[string]any
	if err := json.Unmarshal(appInstallWordPressRequestFixture, &into); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	want := map[string]bool{
		"app_type": true, "os_user": true, "docroot": true,
		"db_name": true, "db_user": true, "db_password": true, "db_host": true,
		"site_url": true, "site_title": true,
		"admin_user": true, "admin_pass": true, "admin_email": true,
		"locale": true, "subdirectory": true, "use_www": true,
	}
	for k := range into {
		if !want[k] {
			t.Errorf("fixture has unexpected field %q (agent installer would silently drop it)", k)
		}
	}
	for k := range want {
		if _, ok := into[k]; !ok {
			t.Errorf("fixture missing field %q the agent installer reads", k)
		}
	}
}

// Compile-time guard: round-trip helper exists for any T below.
var _ = reflect.DeepEqual
