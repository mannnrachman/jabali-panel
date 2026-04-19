package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
	// LastError is set when the filesystem verdict matches intent but the
	// subprocess path surfaced a problem worth flagging — typically an
	// `E: …` line from apt that didn't block install but signals a broken
	// dep graph elsewhere. Empty on clean success.
	LastError string `json:"last_error,omitempty"`
}

// verdictRetryDelay covers the tiny window where dpkg's DB write lags the
// apt exit on slow I/O. One retry after this delay; no retry on the clean
// path (exit 0 already serialized the write).
const verdictRetryDelay = 100 * time.Millisecond

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

	// Pre-action guards that must reject without mutating state.
	switch p.Action {
	case actionRemove:
		if err := guardRemoveSharedPackage(ctx, p.Version, spec); err != nil {
			return nil, err
		}
	case actionEnable:
		if err := requireInstalledBeforeEnable(ctx, p.Version, spec); err != nil {
			return nil, err
		}
	}

	// Run the subprocess chain for the chosen action. We capture BOTH output
	// and any exit error, but never short-circuit on the error — the apt exit
	// code is advisory; the dpkg DB + conf.d symlinks are the verdict. Below,
	// we read fresh state and decide success vs failure on that, flagging any
	// hard apt errors via LastError so the operator still sees the signal.
	var subprocOut bytes.Buffer
	var subprocErr error
	switch p.Action {
	case actionInstall:
		out, err := doAptActionRaw(ctx, p.Version, spec, actionInstall)
		subprocOut.Write(out)
		if err != nil {
			subprocErr = fmt.Errorf("apt-get install: %w", err)
		} else {
			pout, perr := runPhpenmod(ctx, p.Version, spec.EnableName)
			subprocOut.WriteByte('\n')
			subprocOut.Write(pout)
			if perr != nil {
				subprocErr = fmt.Errorf("phpenmod: %w", perr)
			}
		}
	case actionRemove:
		out, err := doAptActionRaw(ctx, p.Version, spec, actionRemove)
		subprocOut.Write(out)
		if err != nil {
			subprocErr = fmt.Errorf("apt-get remove: %w", err)
		}
	case actionEnable:
		out, err := runPhpenmod(ctx, p.Version, spec.EnableName)
		subprocOut.Write(out)
		if err != nil {
			subprocErr = fmt.Errorf("phpenmod: %w", err)
		}
	case actionDisable:
		out, err := runPhpdismod(ctx, p.Version, spec.EnableName)
		subprocOut.Write(out)
		if err != nil {
			subprocErr = fmt.Errorf("phpdismod: %w", err)
		}
	}

	reloadFPMs(ctx, p.Version)

	// Verdict: read fresh filesystem state. Retry once after 100ms if the first
	// read disagrees with intent AND a subprocess errored — covers the window
	// where dpkg's DB write lags the apt exit on slow I/O.
	state, err := readVerdictState(ctx, p.Version, spec)
	if err != nil {
		return nil, err
	}
	if !verdictMatches(state, p.Action) && subprocErr != nil {
		time.Sleep(verdictRetryDelay)
		state, err = readVerdictState(ctx, p.Version, spec)
		if err != nil {
			return nil, err
		}
	}

	if !verdictMatches(state, p.Action) {
		tail := truncateErrorOutput(subprocOut.Bytes())
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("%s %s did not reach intended state: %s", p.Action, p.Ext, tail),
		}
	}

	// Verdict matches intent — operation effectively succeeded. Flag any
	// `E: …` line from apt so the operator still sees the signal even though
	// the filesystem ended up correct.
	var lastError string
	if hasHardAptError(subprocOut.Bytes()) {
		lastError = truncateErrorOutput(subprocOut.Bytes())
		slog.WarnContext(ctx, "php.ext.apply: filesystem verdict matches intent but apt emitted a hard error",
			"version", p.Version, "ext", p.Ext, "action", p.Action, "tail", lastError)
	} else if subprocErr != nil {
		// Benign non-zero — usually a trigger warning. Log INFO for audit, don't surface.
		slog.InfoContext(ctx, "php.ext.apply: subprocess non-zero but verdict matches intent",
			"version", p.Version, "ext", p.Ext, "action", p.Action, "err", subprocErr.Error())
	}

	return phpExtApplyResponse{
		Version:   p.Version,
		Ext:       p.Ext,
		Installed: state.installed,
		Enabled:   state.enabled,
		LastError: lastError,
	}, nil
}

// verdictState captures what the filesystem says about an extension after an
// apply. Callers compare it against the caller's intent to decide pass/fail.
type verdictState struct {
	installed bool
	enabled   bool
}

// readVerdictState reads the authoritative state for (version, ext): dpkg
// package presence + module enabled in BOTH cli and fpm SAPIs. Returns an
// error (wrapping an AgentError with CodeInternal on I/O failure) so callers
// can safely return it through the error interface without triggering the
// typed-nil-in-interface trap.
func readVerdictState(ctx context.Context, version string, spec phpext.Spec) (verdictState, error) {
	pkgs, err := readInstalledPackages(ctx, version)
	if err != nil {
		return verdictState{}, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("dpkg-query: %v", err)}
	}
	mods, err := readEnabledModules(version)
	if err != nil {
		return verdictState{}, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("conf.d glob: %v", err)}
	}
	commonInstalled := pkgs[fmt.Sprintf("php%s-common", version)]
	return verdictState{
		installed: isExtensionInstalled(spec, version, pkgs, commonInstalled),
		enabled:   mods[spec.EnableName],
	}, nil
}

// verdictMatches reports whether the observed state matches the caller's intent.
func verdictMatches(s verdictState, action string) bool {
	switch action {
	case actionInstall:
		return s.installed && s.enabled
	case actionRemove:
		return !s.installed
	case actionEnable:
		return s.enabled
	case actionDisable:
		return !s.enabled
	}
	return false
}

// doAptActionRaw resolves packages + runs apt under aptMu, returning the
// combined output and the raw subprocess error. Callers inspect the error
// in concert with the filesystem verdict — the exit code alone is advisory.
func doAptActionRaw(ctx context.Context, version string, spec phpext.Spec, action string) ([]byte, error) {
	pkgs, err := phpext.ResolvePackages(version, spec.Name)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		// Built-in — shouldn't reach here thanks to earlier guard.
		return nil, fmt.Errorf("no packages to operate on")
	}
	aptMu.Lock()
	defer aptMu.Unlock()
	return runAptGet(ctx, action, pkgs...)
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
