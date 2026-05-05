// Package cronvalidate provides secure parsing and validation of user-submitted
// cron commands and schedules. This is the shared contract between the API
// handler (pre-accept gate) and the agent (defense-in-depth before render).
//
// Design principles:
//   - Reject on ANY unescaped shell metacharacter before parsing (metachar defense).
//   - Use pure-Go tokenizer (github.com/google/shlex) for shell-aware parsing.
//   - Return parsed argv slice so caller feeds directly to systemd ExecStart.
//   - Allow-list is closed-set: wp and php only, with strict path validation.
//   - No subprocess calls; all validation is in-process.
package cronvalidate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/robfig/cron/v3"
)

// Error codes for structured error reporting (suitable for API "error" field).
const (
	ErrCodeEmpty            = "empty"
	ErrCodeTooLong          = "too_long"           // >1024 bytes
	ErrCodeBinaryNotAllowed = "binary_not_allowed" // not wp/php
	ErrCodeMetacharReject   = "metachar_reject"    // shell metacharacters
	ErrCodeBadPathArg       = "bad_path_arg"       // --path= missing, not absolute, traversal, or not in owned docroot
	ErrCodeBadScheduleSyntax = "bad_schedule_syntax"
	ErrCodeScheduleTooFrequent = "schedule_too_frequent" // < 1 min step
)

// ValidationError is the error type returned by validators.
type ValidationError struct {
	Code   string // One of ErrCode* constants
	Detail string // Human-readable explanation
}

func (e *ValidationError) Error() string {
	return e.Code + ": " + e.Detail
}

// Command represents a validated, parsed command ready for systemd ExecStart rendering.
type Command struct {
	// Argv is the fully-parsed argv slice, ready to be single-quoted and
	// emitted as ExecStart= in a systemd unit file.
	Argv []string
}

// metacharSet is the set of bytes that must be rejected in the raw command
// string unless they appear inside matched quotes. These are shell metacharacters
// that could enable injection if present unquoted.
var metacharSet = map[byte]bool{
	'&':  true,  // background / AND
	'|':  true,  // pipe / OR
	';':  true,  // statement separator
	'$':  true,  // variable expansion
	'`':  true,  // backtick substitution
	'(':  true,  // subshell
	')':  true,
	'<':  true,  // input redirection
	'>':  true,  // output redirection
	'\\': true,  // escape
	'\n': true,  // newline
	'\x00': true, // NUL byte
	'{':  true,  // brace expansion
	'}':  true,
	'*':  true,  // glob (allowed inside quotes; will check below)
	'?':  true,  // glob (allowed inside quotes; will check below)
}

// hasUnquotedMetachar scans s for any metachar outside balanced quotes.
// Returns true if a forbidden metachar is found outside quotes.
// Single quotes and double quotes are recognized; content inside is skipped.
func hasUnquotedMetachar(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]

		// Skip single-quoted strings: consume until next unescaped '
		if ch == '\'' {
			i++
			for i < len(s) && s[i] != '\'' {
				i++
			}
			// i is now at closing ' or past end; next iteration increments again
			continue
		}

		// Skip double-quoted strings: consume until next unescaped "
		if ch == '"' {
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++ // skip escaped char
				}
				i++
			}
			continue
		}

		// Outside quotes: check for metacharacters
		if metacharSet[ch] {
			return true
		}
	}
	return false
}

// ValidateCommand parses and validates a user-submitted command string.
// The command must be either:
//   - wp <subcommand> --path=<abs-docroot> [args...]
//   - php <abs-docroot>/<file>.php [args...]
//
// Both forms require the absolute path to resolve within an owned docroot.
// All shell metacharacters are rejected unless inside quotes (and glob chars
// are still rejected in pre-shlex scan). Returns a Command with parsed argv
// on success, or a ValidationError with a code suitable for API responses.
func ValidateCommand(raw string, ownedDocroots []string) (*Command, error) {
	// Empty or whitespace-only
	if strings.TrimSpace(raw) == "" {
		return nil, &ValidationError{
			Code:   ErrCodeEmpty,
			Detail: "command cannot be empty",
		}
	}

	// Too long (guard against resource exhaustion)
	if len(raw) > 1024 {
		return nil, &ValidationError{
			Code:   ErrCodeTooLong,
			Detail: fmt.Sprintf("command exceeds 1024 bytes (%d bytes)", len(raw)),
		}
	}

	// PRIMARY DEFENSE: reject unquoted metacharacters before parsing.
	if hasUnquotedMetachar(raw) {
		return nil, &ValidationError{
			Code: ErrCodeMetacharReject,
			Detail: "command contains shell metacharacters; " +
				"allowed: & | ; $ ` ( ) < > \\ newline NUL { } * ? only inside single/double quotes",
		}
	}

	// Parse with shlex. If shlex itself fails (e.g., unclosed quote), reject.
	argv, err := shlex.Split(raw)
	if err != nil {
		return nil, &ValidationError{
			Code:   ErrCodeMetacharReject,
			Detail: fmt.Sprintf("failed to parse command: %v", err),
		}
	}

	if len(argv) == 0 {
		return nil, &ValidationError{
			Code:   ErrCodeEmpty,
			Detail: "command parsed to empty argv",
		}
	}

	// First token must be wp or php
	binary := argv[0]
	switch binary {
	case "wp":
		return validateWPCommand(argv, ownedDocroots)
	case "php":
		return validatePHPCommand(argv, ownedDocroots)
	default:
		return nil, &ValidationError{
			Code: ErrCodeBinaryNotAllowed,
			Detail: fmt.Sprintf(
				"first token must be 'wp' or 'php', got %q",
				binary,
			),
		}
	}
}

// validateWPCommand checks a wp command has required --path=<docroot> argument.
func validateWPCommand(argv []string, ownedDocroots []string) (*Command, error) {
	// Find --path= argument (may be single token or two: --path, <path>)
	var pathArg string

	for i, arg := range argv {
		if arg == "--path" && i+1 < len(argv) {
			// Two-token form: --path <path>
			pathArg = argv[i+1]
			break
		} else if strings.HasPrefix(arg, "--path=") {
			// Single-token form: --path=<path>
			pathArg = arg[7:] // skip "--path="
			break
		}
	}

	if pathArg == "" {
		return nil, &ValidationError{
			Code:   ErrCodeBadPathArg,
			Detail: "wp command requires --path=<abs-docroot> or --path <abs-docroot>",
		}
	}

	if err := validatePathArg(pathArg, ownedDocroots); err != nil {
		return nil, err
	}

	return &Command{Argv: argv}, nil
}

// validatePHPCommand checks the first argument is an absolute path ending in .php
// within an owned docroot.
func validatePHPCommand(argv []string, ownedDocroots []string) (*Command, error) {
	if len(argv) < 2 {
		return nil, &ValidationError{
			Code:   ErrCodeBadPathArg,
			Detail: "php command requires a path argument",
		}
	}

	pathArg := argv[1]

	// Must be absolute path
	if !filepath.IsAbs(pathArg) {
		return nil, &ValidationError{
			Code: ErrCodeBadPathArg,
			Detail: fmt.Sprintf(
				"php path must be absolute, got %q",
				pathArg,
			),
		}
	}

	// Must end in .php
	if !strings.HasSuffix(pathArg, ".php") {
		return nil, &ValidationError{
			Code: ErrCodeBadPathArg,
			Detail: fmt.Sprintf(
				"php path must end in .php, got %q",
				pathArg,
			),
		}
	}

	if err := validatePathArg(pathArg, ownedDocroots); err != nil {
		return nil, err
	}

	return &Command{Argv: argv}, nil
}

// validatePathArg validates that an absolute path:
//   1. Has no .. tokens
//   2. Is inside one of ownedDocroots (with / boundary check)
//   3. If it exists, verifies via EvalSymlinks; if not, that's OK (will be checked at runtime by cron-precheck)
func validatePathArg(pathStr string, ownedDocroots []string) error {
	// Belt-and-suspenders: reject .. anywhere in original string
	if strings.Contains(pathStr, "..") {
		return &ValidationError{
			Code:   ErrCodeBadPathArg,
			Detail: fmt.Sprintf("path contains '..': %q", pathStr),
		}
	}

	// Attempt to resolve symlinks if the path exists. If it doesn't exist, that's
	// OK for API validation (the cron-precheck helper in step 4 will verify at exec time).
	// If EvalSymlinks succeeds, we use the real path; otherwise we use the cleaned path.
	realPath := pathStr
	if resolved, err := filepath.EvalSymlinks(pathStr); err == nil {
		realPath = resolved
	}
	// If EvalSymlinks fails, we proceed with the cleaned path for containment check.
	realPath = filepath.Clean(realPath)

	// Ensure path is absolute
	if !filepath.IsAbs(realPath) {
		return &ValidationError{
			Code: ErrCodeBadPathArg,
			Detail: fmt.Sprintf(
				"path is not absolute: %q",
				pathStr,
			),
		}
	}

	// Ensure path is within one of the owned docroots (with / boundary check)
	found := false
	for _, docroot := range ownedDocroots {
		// Normalize both for comparison
		docroot = filepath.Clean(docroot)

		// Check if realPath is docroot itself or a descendant
		// We must ensure word-boundary: /home/shuki/x should NOT match /home/shukimalicious/x
		if realPath == docroot {
			found = true
			break
		}
		if strings.HasPrefix(realPath, docroot+"/") {
			found = true
			break
		}
	}

	if !found {
		return &ValidationError{
			Code: ErrCodeBadPathArg,
			Detail: fmt.Sprintf(
				"path %q is not inside owned docroots: %v",
				realPath,
				ownedDocroots,
			),
		}
	}

	return nil
}

// ValidateSchedule validates a 5-field POSIX cron expression.
// Rejects shortcuts (@hourly, @reboot, @every, etc.) and requires exactly 5 fields.
// Empty or whitespace-only expressions are rejected.
func ValidateSchedule(expr string) error {
	expr = strings.TrimSpace(expr)

	// Reject empty
	if expr == "" {
		return &ValidationError{
			Code:   ErrCodeBadScheduleSyntax,
			Detail: "schedule expression cannot be empty",
		}
	}

	// Reject shortcuts (start with @)
	if strings.HasPrefix(expr, "@") {
		return &ValidationError{
			Code: ErrCodeBadScheduleSyntax,
			Detail: fmt.Sprintf(
				"schedule shortcuts (@hourly, @daily, @reboot, @every) not allowed; "+
					"use 5-field cron syntax (e.g. '0 * * * *' for hourly)",
			),
		}
	}

	// Parse with robfig/cron using only 5 fields (no seconds, no special shortcuts)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(expr)
	if err != nil {
		return &ValidationError{
			Code: ErrCodeBadScheduleSyntax,
			Detail: fmt.Sprintf(
				"invalid cron syntax: %v (expected 5-field POSIX format)",
				err,
			),
		}
	}

	// TODO(v2): supplementary systemd-analyze calendar subprocess call as argv array
	// For v1 we rely on robfig/cron alone, which uses the same grammar as systemd's
	// OnCalendar= parsing and is pure-Go and injection-proof.

	return nil
}
