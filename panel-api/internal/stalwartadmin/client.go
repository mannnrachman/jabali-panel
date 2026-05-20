// Package stalwartadmin is a thin Go wrapper over the official
// `stalwart-cli` binary install.sh ships at /usr/local/bin/stalwart-cli
// (see install.sh _install_stalwart_cli). M47 ingest sources call it on
// a 5-minute cadence to poll Stalwart's first-class report objects
// (DmarcExternalReport / TlsExternalReport / ArfExternalReport) — the
// finding from the .150 spike that collapsed Waves 4/6/8 from
// JMAP-mailbox-poll into one pattern (project_stalwart_native_report_storage).
//
// Subprocess wrapper (not HTTP) because:
//  1. Stalwart's REST schema uses HTTP/2 + hash-redirect URLs that
//     change with every schema version — pinning a Go REST client to
//     reverse-engineered endpoints would break on upstream upgrades.
//  2. stalwart-cli is the canonical, upstream-maintained client; it
//     tracks the schema automatically. 5-min cadence × ~3 calls per
//     pass = ~36 subprocess execs/hour — negligible overhead.
//  3. The binary is GUARANTEED to be present on every jabali host
//     (install.sh provisions it) and runs as `jabali` (no privilege
//     escalation needed — Basic auth via STALWART_RECOVERY_ADMIN).
package stalwartadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultBinary is the absolute path install.sh symlinks. Override
// only for tests via Client.Binary.
const DefaultBinary = "/usr/local/bin/stalwart-cli"

// DefaultURL is the loopback admin HTTP endpoint Stalwart binds (per
// M25 unix-socket lockdown — 127.0.0.1 only, no public exposure).
const DefaultURL = "http://127.0.0.1:8446"

// DefaultTimeout caps each CLI invocation. Stalwart's query/get are
// in-memory reads — sub-second at any reasonable report volume — but
// 30s leaves headroom for a large DMARC backlog dump after a long
// outage without stranding the event-source loop on a hung admin port.
const DefaultTimeout = 30 * time.Second

// Client wraps stalwart-cli invocations. Construct via NewClient.
// The struct is intentionally tiny — all knobs come from constructor
// args so config plumbing through serve.go is one block.
type Client struct {
	// Binary is the absolute path to stalwart-cli. Default: DefaultBinary.
	Binary string
	// URL is the admin HTTP endpoint (no trailing slash). Default: DefaultURL.
	URL string
	// User + Password are Basic auth credentials. On jabali hosts
	// these come from STALWART_RECOVERY_ADMIN in /etc/jabali-panel/stalwart.env
	// (the same secret the panel-agent's mail.* commands use).
	User     string
	Password string
	// Timeout is the per-invocation deadline (CLI exec). 0 → DefaultTimeout.
	Timeout time.Duration

	// run is the function that actually runs the binary. Default
	// production impl shells out via exec.CommandContext. Tests inject
	// a fake to return canned stdout/stderr without touching exec.
	run func(ctx context.Context, args []string) (stdout []byte, stderr []byte, err error)
}

// NewClient returns a Client wired to the production subprocess
// runner. Callers should keep one Client around for the process
// lifetime; it's stateless beyond the cached config and a zero-cost
// alloc to construct.
func NewClient(user, password string) *Client {
	c := &Client{
		Binary:   DefaultBinary,
		URL:      DefaultURL,
		User:     user,
		Password: password,
		Timeout:  DefaultTimeout,
	}
	c.run = c.runExec
	return c
}

// Query asks Stalwart for all objects of the given type. The result
// is the raw JSON array stalwart-cli emits (one object per row).
//
// Pass filters as `key:value` strings — stalwart-cli supports `--filter
// receivedAt:>X` etc. The CLI surface accepts:
//
//	query <Type> [--filter <key:op:value>] [--limit N] [--order <field>:<dir>] --json
//
// We don't try to model Stalwart's filter grammar in Go — pass the
// strings through verbatim; callers know the schema for their type
// (use `stalwart-cli describe <Type>` to introspect).
func (c *Client) Query(ctx context.Context, typeName string, filters ...string) (json.RawMessage, error) {
	if err := validateTypeName(typeName); err != nil {
		return nil, err
	}
	args := []string{
		"--url", c.URL,
		"--user", c.User,
		"--password", c.Password,
		"query", typeName, "--json",
	}
	for _, f := range filters {
		if err := validateFilter(f); err != nil {
			return nil, err
		}
		args = append(args, "--filter", f)
	}
	stdout, stderr, err := c.invoke(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("stalwart-cli query %s: %w; stderr=%s", typeName, err, strings.TrimSpace(string(stderr)))
	}
	if len(stdout) == 0 {
		// Empty output means zero rows. Return a JSON empty array so
		// the caller can json.Unmarshal into a slice uniformly.
		return json.RawMessage(`[]`), nil
	}
	return json.RawMessage(stdout), nil
}

// Create POSTs a new object. payload is the JSON value Stalwart's
// schema expects for the type. Returns the upstream-assigned id
// parsed from stalwart-cli's "Created <Type> <id>" stdout. Callers
// persist the id so subsequent updates / deletes target the right
// object. See project_stalwart_mtaouthound_throttle_pin for shapes.
func (c *Client) Create(ctx context.Context, typeName string, payload any) (string, error) {
	if err := validateTypeName(typeName); err != nil {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("stalwartadmin: marshal: %w", err)
	}
	args := []string{
		"--url", c.URL,
		"--user", c.User,
		"--password", c.Password,
		"create", typeName, "--json", string(body),
	}
	stdout, stderr, err := c.invoke(ctx, args)
	if err != nil {
		return "", fmt.Errorf("stalwart-cli create %s: %w; stderr=%s", typeName, err, strings.TrimSpace(string(stderr)))
	}
	out := strings.TrimSpace(string(stdout))
	prefix := "Created " + typeName + " "
	if !strings.HasPrefix(out, prefix) {
		return "", fmt.Errorf("stalwartadmin: unexpected create output %q", out)
	}
	id := strings.TrimSpace(strings.TrimPrefix(out, prefix))
	if id == "" {
		return "", fmt.Errorf("stalwartadmin: empty id in create output %q", out)
	}
	return id, nil
}

// Update mutates an existing object by id. payload may be partial.
func (c *Client) Update(ctx context.Context, typeName, id string, payload any) error {
	if err := validateTypeName(typeName); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("stalwartadmin: marshal: %w", err)
	}
	args := []string{
		"--url", c.URL,
		"--user", c.User,
		"--password", c.Password,
		"update", typeName, id, "--json", string(body),
	}
	_, stderr, err := c.invoke(ctx, args)
	if err != nil {
		return fmt.Errorf("stalwart-cli update %s %s: %w; stderr=%s", typeName, id, err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// Delete removes one object by id. Stalwart's CLI accepts
// comma-separated --ids; we pass a single id to keep the surface narrow.
func (c *Client) Delete(ctx context.Context, typeName, id string) error {
	if err := validateTypeName(typeName); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	args := []string{
		"--url", c.URL,
		"--user", c.User,
		"--password", c.Password,
		"delete", typeName, "--ids", id,
	}
	_, stderr, err := c.invoke(ctx, args)
	if err != nil {
		return fmt.Errorf("stalwart-cli delete %s %s: %w; stderr=%s", typeName, id, err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// Get fetches a single object by id. Pass `singleton` for the singleton
// types (Authentication, MtaSts, etc).
func (c *Client) Get(ctx context.Context, typeName, id string) (json.RawMessage, error) {
	if err := validateTypeName(typeName); err != nil {
		return nil, err
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	args := []string{
		"--url", c.URL,
		"--user", c.User,
		"--password", c.Password,
		"get", typeName, id, "--json",
	}
	stdout, stderr, err := c.invoke(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("stalwart-cli get %s %s: %w; stderr=%s", typeName, id, err, strings.TrimSpace(string(stderr)))
	}
	return json.RawMessage(stdout), nil
}

// invoke is the timeout-bounded run shim. Centralises the cancellation
// + timeout so every public method gets it without restating.
func (c *Client) invoke(ctx context.Context, args []string) ([]byte, []byte, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.run(cctx, args)
}

// runExec is the production subprocess runner. Captures stdout + stderr
// separately so error contexts surface the actual stalwart-cli failure
// message rather than a bare exit-code-1.
func (c *Client) runExec(ctx context.Context, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return []byte(stdout.String()), []byte(stderr.String()), err
}

// validateTypeName rejects anything that doesn't look like a Stalwart
// schema type (CamelCase, [A-Za-z][A-Za-z0-9]*). Defense-in-depth —
// the type name eventually becomes an argv string the CLI passes to
// the admin REST URL, so injection here would map to URL injection.
func validateTypeName(t string) error {
	if t == "" {
		return errors.New("stalwartadmin: empty type name")
	}
	first := t[0]
	if !(first >= 'A' && first <= 'Z') {
		return fmt.Errorf("stalwartadmin: type name %q must be CamelCase", t)
	}
	for _, r := range t {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("stalwartadmin: type name %q contains illegal char %q", t, r)
		}
	}
	return nil
}

// validateID accepts the `singleton` literal or anything that looks
// like a Stalwart id (alphanumeric + hyphen/underscore, no path
// separators or shell-metas). Stalwart ids are short opaque tokens
// (e.g. "b" for the first Domain on .150) so the regex is generous.
func validateID(id string) error {
	if id == "" {
		return errors.New("stalwartadmin: empty id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("stalwartadmin: id %q contains illegal char %q", id, r)
		}
	}
	return nil
}

// validateFilter prevents an attacker-controlled filter from injecting
// extra argv (the args go via exec.Command, which doesn't shell-expand,
// but a filter like `--password=foo` could still poison the arg list).
// Allowed: alphanumeric + `:` (separator) + `.` + `-` + `_` + comparators.
func validateFilter(f string) error {
	if f == "" {
		return errors.New("stalwartadmin: empty filter")
	}
	if strings.HasPrefix(f, "-") {
		return fmt.Errorf("stalwartadmin: filter %q starts with '-' (looks like a flag)", f)
	}
	for _, r := range f {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == ':' || r == '.' || r == '-' ||
			r == '_' || r == '@' || r == '<' || r == '>' || r == '='
		if !ok {
			return fmt.Errorf("stalwartadmin: filter %q contains illegal char %q", f, r)
		}
	}
	return nil
}
