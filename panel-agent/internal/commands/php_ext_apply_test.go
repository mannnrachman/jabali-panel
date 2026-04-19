package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestApply_InstallCurl(t *testing.T) {
	var aptCalls [][]string
	var enmodCalls [][2]string
	installTestFixtures(t, phpExtTestFixtures{
		installed:  []string{"8.5"},
		dpkgOut:    []byte("php8.5-common\tinstall ok installed\nphp8.5-curl\tinstall ok installed\n"),
		confDOut:   []string{"/etc/php/8.5/fpm/conf.d/20-curl.ini"},
		aptCalls:   &aptCalls,
		enmodCalls: &enmodCalls,
	})

	raw, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "install"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := raw.(phpExtApplyResponse)
	if !resp.Installed || !resp.Enabled || resp.Ext != "curl" || resp.Version != "8.5" {
		t.Fatalf("response = %+v", resp)
	}
	if len(aptCalls) != 1 || aptCalls[0][0] != "install" || aptCalls[0][1] != "php8.5-curl" {
		t.Fatalf("aptCalls = %v", aptCalls)
	}
	if len(enmodCalls) != 1 || enmodCalls[0] != [2]string{"8.5", "curl"} {
		t.Fatalf("enmodCalls = %v", enmodCalls)
	}
}

func TestApply_InstallMysqlSinglePackage(t *testing.T) {
	var aptCalls [][]string
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		dpkgOut:   []byte("php8.5-common\tinstall ok installed\nphp8.5-mysql\tinstall ok installed\n"),
		confDOut:  nil,
		aptCalls:  &aptCalls,
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "mysql", Action: "install"}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// php<v>-mysql is the meta-package; should be passed exactly once, no duplicate.
	if len(aptCalls) != 1 || len(aptCalls[0]) != 2 || aptCalls[0][1] != "php8.5-mysql" {
		t.Fatalf("aptCalls = %v", aptCalls)
	}
}

func TestApply_InstallBuiltIn_Rejected(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "posix", Action: "install"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeInvalidArgument || !strings.Contains(ae.Message, "built in") {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_EnableMysql_Ambiguous(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "mysql", Action: "enable"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeInvalidArgument || !strings.Contains(ae.Message, "ambiguous") {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_RemoveCurl(t *testing.T) {
	var aptCalls [][]string
	var dismodCalls [][2]string
	installTestFixtures(t, phpExtTestFixtures{
		installed:   []string{"8.5"},
		dpkgOut:     []byte("php8.5-common\tinstall ok installed\nphp8.5-curl\tinstall ok installed\n"),
		confDOut:    nil,
		aptCalls:    &aptCalls,
		dismodCalls: &dismodCalls,
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "remove"}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(aptCalls) != 1 || aptCalls[0][0] != "remove" {
		t.Fatalf("aptCalls = %v", aptCalls)
	}
	// remove must NOT invoke phpdismod — apt removal kills the symlinks.
	if len(dismodCalls) != 0 {
		t.Fatalf("phpdismod should not be called on remove, got %v", dismodCalls)
	}
}

func TestApply_RemoveXmlWhileDomInstalled_Blocked(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		// Both xml and dom map to the same php8.5-xml pkg. If it's present,
		// dom is considered "installed". So removing xml must be blocked.
		dpkgOut: []byte("php8.5-common\tinstall ok installed\nphp8.5-xml\tinstall ok installed\n"),
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "xml", Action: "remove"}))
	ae := asAgentErr(t, err)
	if ae.Code != agentwire.CodeFailedPrecondition || !strings.Contains(ae.Message, "still in use") {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_EnableOpcache_NoApt(t *testing.T) {
	var aptCalls [][]string
	var enmodCalls [][2]string
	installTestFixtures(t, phpExtTestFixtures{
		installed:  []string{"8.5"},
		dpkgOut:    []byte("php8.5-common\tinstall ok installed\n"),
		confDOut:   []string{"/etc/php/8.5/fpm/conf.d/10-opcache.ini"},
		aptCalls:   &aptCalls,
		enmodCalls: &enmodCalls,
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "opcache", Action: "enable"}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(aptCalls) != 0 {
		t.Fatalf("apt must not run for enable, got %v", aptCalls)
	}
	if len(enmodCalls) != 1 || enmodCalls[0] != [2]string{"8.5", "opcache"} {
		t.Fatalf("enmodCalls = %v", enmodCalls)
	}
}

func TestApply_EnableNotInstalled_Rejected(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		dpkgOut:   []byte("php8.5-common\tinstall ok installed\n"),
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "enable"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeFailedPrecondition {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_DisableOpcache(t *testing.T) {
	var dismodCalls [][2]string
	installTestFixtures(t, phpExtTestFixtures{
		installed:   []string{"8.5"},
		dpkgOut:     []byte(""),
		dismodCalls: &dismodCalls,
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "opcache", Action: "disable"}))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(dismodCalls) != 1 || dismodCalls[0] != [2]string{"8.5", "opcache"} {
		t.Fatalf("dismodCalls = %v", dismodCalls)
	}
}

func TestApply_BadAction(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "frobnicate"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_UnknownExt(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "nope", Action: "install"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_VersionNotInstalled(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.4"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "install"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeFailedPrecondition {
		t.Fatalf("got %+v", ae)
	}
}

func TestApply_AptFailureTruncated(t *testing.T) {
	big := strings.Repeat("E", 1200)
	installTestFixtures(t, phpExtTestFixtures{
		installed: []string{"8.5"},
		aptOut:    []byte(big),
		aptErr:    errors.New("exit 100"),
	})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5", Ext: "curl", Action: "install"}))
	ae := asAgentErr(t, err)
	if ae.Code != agentwire.CodeFailedPrecondition {
		t.Fatalf("code = %s", ae.Code)
	}
	// truncateErrorOutput caps at 512 + a trailing …
	if len(ae.Message) > 600 {
		t.Fatalf("stderr not truncated: len=%d", len(ae.Message))
	}
}

func TestApply_BadVersionFormat(t *testing.T) {
	installTestFixtures(t, phpExtTestFixtures{installed: []string{"8.5"}})
	_, err := phpExtApplyHandler(context.Background(),
		mustJSON(t, phpExtApplyParams{Version: "8.5.1", Ext: "curl", Action: "install"}))
	if ae := asAgentErr(t, err); ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("got %+v", ae)
	}
}
