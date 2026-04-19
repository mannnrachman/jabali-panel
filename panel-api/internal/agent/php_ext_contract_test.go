// This file is the authoritative wire contract for php.ext.list and
// php.ext.apply between panel-api and panel-agent. Any divergence between
// this file and the handlers in panel-agent/internal/commands/php_ext_*.go
// is a bug in the handlers. Do not drift either side without updating the
// fixtures under testdata/ first — Step 3 of plans/php-extensions-tab.md
// and the UI in Step 4 consume these exact shapes.
package agent

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"
)

//go:embed testdata/php_ext_list_request.json
var phpExtListRequestFixture []byte

//go:embed testdata/php_ext_list_response.json
var phpExtListResponseFixture []byte

//go:embed testdata/php_ext_apply_install_request.json
var phpExtApplyInstallRequestFixture []byte

//go:embed testdata/php_ext_apply_install_response.json
var phpExtApplyInstallResponseFixture []byte

//go:embed testdata/php_ext_apply_enable_request.json
var phpExtApplyEnableRequestFixture []byte

//go:embed testdata/php_ext_apply_enable_response.json
var phpExtApplyEnableResponseFixture []byte

// PHPExtensionState is the per-ext record returned inside list and apply responses.
type PHPExtensionState struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	BuiltIn   bool   `json:"built_in"`
}

type PHPExtListRequest struct {
	Version string `json:"version"`
}

type PHPExtListResponse struct {
	Version    string              `json:"version"`
	Extensions []PHPExtensionState `json:"extensions"`
}

type PHPExtApplyRequest struct {
	Version string `json:"version"`
	Ext     string `json:"ext"`
	// Action is one of: install, remove, enable, disable.
	Action string `json:"action"`
}

type PHPExtApplyResponse struct {
	Version   string `json:"version"`
	Ext       string `json:"ext"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
}

// roundTrip proves the fixture and the typed struct are semantically equal:
// unmarshal the fixture into T, marshal back, re-parse as generic JSON, and
// compare against the generic JSON parse of the raw fixture via reflect.DeepEqual.
// A mismatch means the struct dropped a field or introduced one the fixture
// doesn't have — a contract break. We compare via map-of-any so key order
// doesn't matter; we only care that the same leaves round-trip.
func roundTrip[T any](t *testing.T, raw []byte) {
	t.Helper()
	var typed T
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatalf("unmarshal %T: %v", typed, err)
	}
	remarshaled, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("remarshal %T: %v", typed, err)
	}
	var gotAny any
	if err := json.Unmarshal(remarshaled, &gotAny); err != nil {
		t.Fatalf("re-unmarshal %T: %v", typed, err)
	}
	var wantAny any
	if err := json.Unmarshal(raw, &wantAny); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		gotPretty, _ := json.MarshalIndent(gotAny, "", "  ")
		wantPretty, _ := json.MarshalIndent(wantAny, "", "  ")
		t.Fatalf("%T round-trip mismatch\nwant:\n%s\ngot:\n%s", typed, wantPretty, gotPretty)
	}
}

func TestPHPExtListRequest_RoundTrips(t *testing.T) {
	roundTrip[PHPExtListRequest](t, phpExtListRequestFixture)
}

func TestPHPExtListResponse_RoundTrips(t *testing.T) {
	roundTrip[PHPExtListResponse](t, phpExtListResponseFixture)
}

func TestPHPExtApplyInstallRequest_RoundTrips(t *testing.T) {
	roundTrip[PHPExtApplyRequest](t, phpExtApplyInstallRequestFixture)
}

func TestPHPExtApplyInstallResponse_RoundTrips(t *testing.T) {
	roundTrip[PHPExtApplyResponse](t, phpExtApplyInstallResponseFixture)
}

func TestPHPExtApplyEnableRequest_RoundTrips(t *testing.T) {
	roundTrip[PHPExtApplyRequest](t, phpExtApplyEnableRequestFixture)
}

func TestPHPExtApplyEnableResponse_RoundTrips(t *testing.T) {
	roundTrip[PHPExtApplyResponse](t, phpExtApplyEnableResponseFixture)
}
