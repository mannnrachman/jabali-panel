package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext"
)

type phpExtListParams struct {
	Version string `json:"version"`
}

type phpExtItem struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	BuiltIn   bool   `json:"built_in"`
}

type phpExtListResponse struct {
	Version    string       `json:"version"`
	Extensions []phpExtItem `json:"extensions"`
}

func phpExtListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p phpExtListParams
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "version parameter required"}
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if !phpext.ValidVersion(p.Version) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("invalid version %q", p.Version)}
	}

	installed, err := listInstalledPHPVersionsFunc()
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if !containsString(installed, p.Version) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("PHP %s is not installed", p.Version)}
	}

	installedPkgs, err := readInstalledPackages(ctx, p.Version)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("dpkg-query: %v", err)}
	}
	enabledMods, err := readEnabledModules(p.Version)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("conf.d glob: %v", err)}
	}

	specs := phpext.All()
	items := make([]phpExtItem, 0, len(specs))
	commonInstalled := installedPkgs[fmt.Sprintf("php%s-common", p.Version)]
	for _, s := range specs {
		items = append(items, phpExtItem{
			Name:      s.Name,
			BuiltIn:   s.BuiltIn,
			Installed: isExtensionInstalled(s, p.Version, installedPkgs, commonInstalled),
			Enabled:   enabledMods[s.EnableName],
		})
	}
	return phpExtListResponse{Version: p.Version, Extensions: items}, nil
}

// isExtensionInstalled reports whether the package(s) backing spec are present.
// Built-ins ride on php<v>-common; non-built-ins require every package in Spec.Packages.
func isExtensionInstalled(s phpext.Spec, version string, pkgs map[string]bool, commonInstalled bool) bool {
	if s.BuiltIn {
		return commonInstalled
	}
	resolved, err := phpext.ResolvePackages(version, s.Name)
	if err != nil || len(resolved) == 0 {
		return false
	}
	for _, p := range resolved {
		if !pkgs[p] {
			return false
		}
	}
	return true
}

// readInstalledPackages runs one dpkg-query call for `php<v>-*` and returns
// a set of package names whose status line contains " ok installed".
func readInstalledPackages(ctx context.Context, version string) (map[string]bool, error) {
	out, err := runDpkgQuery(ctx, fmt.Sprintf("php%s-*", version))
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.Contains(parts[1], "install ok installed") {
			set[parts[0]] = true
		}
	}
	return set, nil
}

// readEnabledModules returns the set of module names enabled in BOTH the CLI
// and FPM SAPIs. phpenmod (run by dpkg postinst or our explicit call) writes
// both symlinks in lockstep; a module present in only one SAPI indicates
// operator-surface drift (manual phpdismod -s fpm, broken symlink, partial
// package removal). Requiring both is the conservative read — list callers
// and the apply verdict see identical truth.
func readEnabledModules(version string) (map[string]bool, error) {
	fpm, err := readSAPIEnabled(version, "fpm")
	if err != nil {
		return nil, err
	}
	cli, err := readSAPIEnabled(version, "cli")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for mod := range fpm {
		if cli[mod] {
			out[mod] = true
		}
	}
	return out, nil
}

// readSAPIEnabled scans /etc/php/<v>/<sapi>/conf.d/*.ini — filenames are
// typically `NN-<module>.ini` (symlinks to mods-available). Returns a set
// keyed by module base name (everything after the leading digits-dash prefix,
// minus the .ini suffix).
func readSAPIEnabled(version, sapi string) (map[string]bool, error) {
	matches, err := globConfD(version, sapi)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range matches {
		base := filepath.Base(m)
		base = strings.TrimSuffix(base, ".ini")
		// Strip leading NN- prefix (e.g. "20-bcmath" → "bcmath").
		if i := strings.Index(base, "-"); i >= 0 && i < len(base)-1 {
			base = base[i+1:]
		}
		out[base] = true
	}
	return out, nil
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func init() {
	Default.Register("php.ext.list", phpExtListHandler)
}
