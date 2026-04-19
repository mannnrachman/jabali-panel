package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext"
)

// phpExtTestFixtures swaps the package-level function vars for the duration
// of a test and restores them on cleanup. Every subprocess variable is
// overridden with a no-op or a caller-supplied fake.
type phpExtTestFixtures struct {
	installed    []string
	installedErr error
	dpkgOut      []byte
	dpkgErr      error
	confDOut     []string // mirrored to both cli and fpm unless overridden
	confDFpmOut  []string // overrides confDOut for fpm only
	confDCliOut  []string // overrides confDOut for cli only
	confDErr     error
	aptCalls     *[][]string
	aptOut       []byte
	aptErr       error
	enmodCalls   *[][2]string
	enmodOut     []byte
	enmodErr     error
	dismodCalls  *[][2]string
	dismodOut    []byte
	dismodErr    error
	reloadCalls  *int
}

func installTestFixtures(t *testing.T, f phpExtTestFixtures) {
	t.Helper()
	oldInst, oldDpkg, oldGlob := listInstalledPHPVersionsFunc, runDpkgQuery, globConfD
	oldApt, oldEn, oldDis, oldReload := runAptGet, runPhpenmod, runPhpdismod, reloadFPMs
	t.Cleanup(func() {
		listInstalledPHPVersionsFunc = oldInst
		runDpkgQuery = oldDpkg
		globConfD = oldGlob
		runAptGet = oldApt
		runPhpenmod = oldEn
		runPhpdismod = oldDis
		reloadFPMs = oldReload
	})
	listInstalledPHPVersionsFunc = func() ([]string, error) { return f.installed, f.installedErr }
	runDpkgQuery = func(ctx context.Context, pattern string) ([]byte, error) { return f.dpkgOut, f.dpkgErr }
	// Dual-SAPI conf.d: fake returns the same set for both cli and fpm so
	// "enabled" (both-sapis-symlinked) matches the single slice fixtures
	// supply. Drift tests use confDFpmOut / confDCliOut explicitly.
	globConfD = func(version, sapi string) ([]string, error) {
		if f.confDErr != nil {
			return nil, f.confDErr
		}
		switch sapi {
		case "fpm":
			if f.confDFpmOut != nil {
				return f.confDFpmOut, nil
			}
			return f.confDOut, nil
		case "cli":
			if f.confDCliOut != nil {
				return f.confDCliOut, nil
			}
			return f.confDOut, nil
		}
		return nil, nil
	}
	runAptGet = func(ctx context.Context, action string, pkgs ...string) ([]byte, error) {
		if f.aptCalls != nil {
			*f.aptCalls = append(*f.aptCalls, append([]string{action}, pkgs...))
		}
		return f.aptOut, f.aptErr
	}
	runPhpenmod = func(ctx context.Context, v, m string) ([]byte, error) {
		if f.enmodCalls != nil {
			*f.enmodCalls = append(*f.enmodCalls, [2]string{v, m})
		}
		return f.enmodOut, f.enmodErr
	}
	runPhpdismod = func(ctx context.Context, v, m string) ([]byte, error) {
		if f.dismodCalls != nil {
			*f.dismodCalls = append(*f.dismodCalls, [2]string{v, m})
		}
		return f.dismodOut, f.dismodErr
	}
	reloadFPMs = func(ctx context.Context, version string) {
		if f.reloadCalls != nil {
			*f.reloadCalls++
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func asAgentErr(t *testing.T, err error) *agentwire.AgentError {
	t.Helper()
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T %v", err, err)
	}
	return ae
}

func TestList_BadVersionFormat(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtListHandler(context.Background(), mustJSON(t, phpExtListParams{Version: "8"}))
	if got := asAgentErr(t, err).Code; got != agentwire.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", got, agentwire.CodeInvalidArgument)
	}
}

func TestList_VersionNotInstalled(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.4"}})
	_, err := phpExtListHandler(context.Background(), mustJSON(t, phpExtListParams{Version: "8.5"}))
	if got := asAgentErr(t, err).Code; got != agentwire.CodeFailedPrecondition {
		t.Fatalf("code = %q, want %q", got, agentwire.CodeFailedPrecondition)
	}
}

func TestList_EmptyHost(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		dpkgOut:   []byte(""),
		confDOut:  nil,
	})
	raw, err := phpExtListHandler(context.Background(), mustJSON(t, phpExtListParams{Version: "8.5"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := raw.(phpExtListResponse)
	if len(resp.Extensions) != len(phpext.All()) {
		t.Fatalf("len(extensions) = %d, want %d", len(resp.Extensions), len(phpext.All()))
	}
	for _, e := range resp.Extensions {
		if e.Installed || e.Enabled {
			t.Fatalf("ext %s should be installed=false enabled=false on empty host, got %+v", e.Name, e)
		}
	}
}

func TestList_BuiltInPiggybacksOnCommon(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		dpkgOut:   []byte("php8.5-common\tinstall ok installed\n"),
		confDOut:  []string{"/etc/php/8.5/fpm/conf.d/20-posix.ini"},
	})
	raw, _ := phpExtListHandler(context.Background(), mustJSON(t, phpExtListParams{Version: "8.5"}))
	resp := raw.(phpExtListResponse)
	var posix phpExtItem
	for _, e := range resp.Extensions {
		if e.Name == "posix" {
			posix = e
			break
		}
	}
	if !posix.BuiltIn || !posix.Installed || !posix.Enabled {
		t.Fatalf("posix expected built-in+installed+enabled, got %+v", posix)
	}
}

func TestList_CurlInstalledAndEnabled(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		dpkgOut:   []byte("php8.5-common\tinstall ok installed\nphp8.5-curl\tinstall ok installed\n"),
		confDOut:  []string{"/etc/php/8.5/fpm/conf.d/20-curl.ini"},
	})
	raw, _ := phpExtListHandler(context.Background(), mustJSON(t, phpExtListParams{Version: "8.5"}))
	resp := raw.(phpExtListResponse)
	for _, e := range resp.Extensions {
		if e.Name == "curl" {
			if !e.Installed || !e.Enabled {
				t.Fatalf("curl: want installed+enabled, got %+v", e)
			}
			return
		}
	}
	t.Fatal("curl not in response")
}

func TestList_MissingParams(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtListHandler(context.Background(), nil)
	if got := asAgentErr(t, err).Code; got != agentwire.CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", got, agentwire.CodeInvalidArgument)
	}
}
