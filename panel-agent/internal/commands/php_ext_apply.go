package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext"
)

type phpExtApplyParams struct {
	Version string `json:"version"`
	Ext     string `json:"ext"`
	Action  string `json:"action"`
}

type phpExtApplyResponse struct {
	Version   string `json:"version"`
	Ext       string `json:"ext"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
}

const (
	actionInstall = "install"
	actionRemove  = "remove"
	actionEnable  = "enable"
	actionDisable = "disable"
)

func phpExtApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p phpExtApplyParams
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if !phpext.ValidVersion(p.Version) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("invalid version %q", p.Version)}
	}
	spec, ok := phpext.Lookup(p.Ext)
	if !ok {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("unknown extension %q", p.Ext)}
	}
	switch p.Action {
	case actionInstall, actionRemove, actionEnable, actionDisable:
	default:
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("invalid action %q", p.Action)}
	}
	if (p.Action == actionInstall || p.Action == actionRemove) && spec.BuiltIn {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("extension %s is built in; use enable/disable", p.Ext)}
	}
	if (p.Action == actionEnable || p.Action == actionDisable) && p.Ext == "mysql" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "ambiguous target; enable/disable mysqli or pdo_mysql directly"}
	}

	installed, err := listInstalledPHPVersionsFunc()
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	if !containsString(installed, p.Version) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("PHP %s is not installed", p.Version)}
	}

	switch p.Action {
	case actionInstall:
		if err := doAptAction(ctx, p.Version, spec, actionInstall); err != nil {
			return nil, err
		}
		if out, err := runPhpenmod(ctx, p.Version, spec.EnableName); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("phpenmod: %s", truncateErrorOutput(out))}
		}
	case actionRemove:
		if err := guardRemoveSharedPackage(ctx, p.Version, spec); err != nil {
			return nil, err
		}
		if err := doAptAction(ctx, p.Version, spec, actionRemove); err != nil {
			return nil, err
		}
	case actionEnable:
		if err := requireInstalledBeforeEnable(ctx, p.Version, spec); err != nil {
			return nil, err
		}
		if out, err := runPhpenmod(ctx, p.Version, spec.EnableName); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("phpenmod: %s", truncateErrorOutput(out))}
		}
	case actionDisable:
		if out, err := runPhpdismod(ctx, p.Version, spec.EnableName); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("phpdismod: %s", truncateErrorOutput(out))}
		}
	}

	reloadFPMs(ctx, p.Version)

	// Fresh state read-back so the caller sees reality, not intent.
	pkgs, err := readInstalledPackages(ctx, p.Version)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("dpkg-query: %v", err)}
	}
	mods, err := readEnabledModules(p.Version)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("conf.d glob: %v", err)}
	}
	commonInstalled := pkgs[fmt.Sprintf("php%s-common", p.Version)]
	return phpExtApplyResponse{
		Version:   p.Version,
		Ext:       p.Ext,
		Installed: isExtensionInstalled(spec, p.Version, pkgs, commonInstalled),
		Enabled:   mods[spec.EnableName],
	}, nil
}

// doAptAction resolves packages + runs apt under aptMu.
func doAptAction(ctx context.Context, version string, spec phpext.Spec, action string) error {
	pkgs, err := phpext.ResolvePackages(version, spec.Name)
	if err != nil {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if len(pkgs) == 0 {
		// Built-in — shouldn't reach here thanks to earlier guard.
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "no packages to operate on"}
	}
	aptMu.Lock()
	defer aptMu.Unlock()
	out, err := runAptGet(ctx, action, pkgs...)
	if err != nil {
		return &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition, Message: fmt.Sprintf("apt-get %s: %s", action, truncateErrorOutput(out))}
	}
	return nil
}

// guardRemoveSharedPackage rejects the remove if any OTHER non-removed allowlist
// ext resolves to the same apt package AND is currently installed. This catches
// `remove xml` when `dom` (also backed by php<v>-xml) is still in use.
func guardRemoveSharedPackage(ctx context.Context, version string, spec phpext.Spec) error {
	mine, err := phpext.ResolvePackages(version, spec.Name)
	if err != nil || len(mine) == 0 {
		return nil
	}
	mineSet := map[string]bool{}
	for _, m := range mine {
		mineSet[m] = true
	}
	pkgs, err := readInstalledPackages(ctx, version)
	if err != nil {
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("dpkg-query: %v", err)}
	}
	for _, other := range phpext.All() {
		if other.Name == spec.Name || other.BuiltIn {
			continue
		}
		otherPkgs, err := phpext.ResolvePackages(version, other.Name)
		if err != nil {
			continue
		}
		sharesPkg := false
		for _, op := range otherPkgs {
			if mineSet[op] {
				sharesPkg = true
				break
			}
		}
		if !sharesPkg {
			continue
		}
		// Is the other ext currently installed? If so, removing our shared
		// packages would yank it out from under them.
		allOtherPresent := true
		for _, op := range otherPkgs {
			if !pkgs[op] {
				allOtherPresent = false
				break
			}
		}
		if allOtherPresent {
			return &agentwire.AgentError{
				Code:    agentwire.CodeFailedPrecondition,
				Message: fmt.Sprintf("cannot remove %s: still in use by %s", spec.Name, other.Name),
			}
		}
	}
	return nil
}

// requireInstalledBeforeEnable rejects enable when the module ini isn't
// present in mods-available/ (i.e. the apt package isn't installed for this version).
func requireInstalledBeforeEnable(ctx context.Context, version string, spec phpext.Spec) error {
	pkgs, err := readInstalledPackages(ctx, version)
	if err != nil {
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("dpkg-query: %v", err)}
	}
	commonInstalled := pkgs[fmt.Sprintf("php%s-common", version)]
	if !isExtensionInstalled(spec, version, pkgs, commonInstalled) {
		return &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("extension %s is not installed for PHP %s", spec.Name, version),
		}
	}
	return nil
}

func init() {
	Default.Register("php.ext.apply", phpExtApplyHandler)
}
