package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the local restic repository path provisioned by
// install_backup_foundation in install.sh. M30.1 adds remote support
// by switching this to an `s3:…` / `sftp:…` / `b2:…` URL via
// ResticConfig.Repo.
const DefaultRepo = "/var/lib/jabali-backups/repo"

// DefaultPasswordFile mirrors the path created by install.sh.
const DefaultPasswordFile = "/etc/jabali-panel/restic-repo.password"

// ResticConfig is the wrapper-wide configuration. Repo + PasswordFile
// are mandatory; ExtraEnv lets callers inject AWS_ACCESS_KEY_ID etc.
// for remote backends without baking them into the wrapper.
type ResticConfig struct {
	Repo         string
	PasswordFile string
	ExtraEnv     []string // KEY=VALUE; passed to exec.Cmd.Env in addition to os.Environ
	Bin          string   // default "restic"; override for tests
	// Runner intercepts every CLI invocation. Production uses
	// realRunner (exec.CommandContext); tests inject a fake.
	Runner Runner
}

// DefaultConfig returns a ResticConfig pointing at the local repo.
func DefaultConfig() ResticConfig {
	return ResticConfig{
		Repo:         DefaultRepo,
		PasswordFile: DefaultPasswordFile,
		Bin:          "restic",
		Runner:       realRunner{},
	}
}

// Runner abstracts the subprocess boundary so unit tests don't shell
// out to a real restic binary.
type Runner interface {
	Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader) (stdout, stderr []byte, err error)
}

type realRunner struct{}

func (realRunner) Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Client is the typed restic CLI wrapper. Construct via New(cfg).
type Client struct {
	cfg ResticConfig
}

// New returns a configured Client. Panics if Repo or PasswordFile is
// empty (programmer error — every call site supplies these).
func New(cfg ResticConfig) *Client {
	if cfg.Repo == "" {
		panic("backup.New: Repo is empty")
	}
	if cfg.PasswordFile == "" {
		panic("backup.New: PasswordFile is empty")
	}
	if cfg.Bin == "" {
		cfg.Bin = "restic"
	}
	if cfg.Runner == nil {
		cfg.Runner = realRunner{}
	}
	return &Client{cfg: cfg}
}

// baseArgs returns the universal flags every restic invocation carries.
func (c *Client) baseArgs() []string {
	return []string{
		"--repo", c.cfg.Repo,
		"--password-file", c.cfg.PasswordFile,
	}
}

// run executes restic with the given subcommand args and stdin, prepending
// baseArgs and appending --json where applicable. Stderr is captured
// separately so callers can attach it to error envelopes.
func (c *Client) run(ctx context.Context, args []string, stdin io.Reader) ([]byte, []byte, error) {
	full := append(c.baseArgs(), args...)
	stdout, stderr, err := c.cfg.Runner.Run(ctx, c.cfg.Bin, full, c.cfg.ExtraEnv, stdin)
	if err != nil {
		return stdout, stderr, fmt.Errorf("restic %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(stderr)))
	}
	return stdout, stderr, nil
}

// Snapshot is a parsed `restic snapshots --json` row.
type Snapshot struct {
	ID         string    `json:"id"`
	ShortID    string    `json:"short_id,omitempty"`
	Time       time.Time `json:"time"`
	Hostname   string    `json:"hostname"`
	Username   string    `json:"username,omitempty"`
	Tags       []string  `json:"tags"`
	Paths      []string  `json:"paths"`
	Parent     string    `json:"parent,omitempty"`
	ProgramVer string    `json:"program_version,omitempty"`
}

// Snapshots lists snapshots. Optional tags filter narrows the result
// server-side via repeated --tag=key=value flags.
func (c *Client) Snapshots(ctx context.Context, tags []Tag) ([]Snapshot, error) {
	args := []string{"snapshots", "--json"}
	for _, t := range tags {
		args = append(args, "--tag", string(t))
	}
	out, _, err := c.run(ctx, args, nil)
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out, &snaps); err != nil {
		// Empty repo prints `null`; treat as empty list.
		if bytes.Equal(bytes.TrimSpace(out), []byte("null")) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	return snaps, nil
}

// BackupSummary is the JSON body restic emits at the end of `backup --json`.
// Fields restic emits are listed at https://restic.readthedocs.io/en/latest/075_scripting.html
type BackupSummary struct {
	MessageType         string  `json:"message_type"` // "summary"
	FilesNew            uint64  `json:"files_new"`
	FilesChanged        uint64  `json:"files_changed"`
	FilesUnmodified     uint64  `json:"files_unmodified"`
	DirsNew             uint64  `json:"dirs_new"`
	DirsChanged         uint64  `json:"dirs_changed"`
	DirsUnmodified      uint64  `json:"dirs_unmodified"`
	DataAdded           uint64  `json:"data_added"`
	TotalFilesProcessed uint64  `json:"total_files_processed"`
	TotalBytesProcessed uint64  `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`
}

// BackupOpts controls one `restic backup` invocation.
type BackupOpts struct {
	Paths        []string  // file/dir paths; ignored when Stdin is set
	Stdin        io.Reader // when non-nil, runs `--stdin --stdin-filename=<StdinName>`
	StdinName    string    // virtual filename for stdin snapshots
	Tags         []Tag     // appended via --tag flags
	ExcludeFile  string    // path to --exclude-file
	ExcludeArgs  []string  // additional --exclude=PATTERN
	Hostname     string    // override --host (default: real hostname)
}

// Backup runs `restic backup` with the given options. Returns the
// parsed summary (snapshot_id + bytes_added).
func (c *Client) Backup(ctx context.Context, opts BackupOpts) (*BackupSummary, error) {
	args := []string{"backup", "--json"}
	for _, t := range opts.Tags {
		args = append(args, "--tag", string(t))
	}
	if opts.Hostname != "" {
		args = append(args, "--host", opts.Hostname)
	}
	if opts.ExcludeFile != "" {
		args = append(args, "--exclude-file", opts.ExcludeFile)
	}
	for _, p := range opts.ExcludeArgs {
		args = append(args, "--exclude", p)
	}
	if opts.Stdin != nil {
		args = append(args, "--stdin")
		if opts.StdinName != "" {
			args = append(args, "--stdin-filename", opts.StdinName)
		}
	} else {
		if len(opts.Paths) == 0 {
			return nil, errors.New("backup: Paths or Stdin required")
		}
		args = append(args, opts.Paths...)
	}
	stdout, _, err := c.run(ctx, args, opts.Stdin)
	if err != nil {
		return nil, err
	}
	// `backup --json` emits one JSON object per line; the last line is
	// the summary. Walk lines until we find message_type=summary.
	var summary *BackupSummary
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe struct {
			MessageType string `json:"message_type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue // skip non-JSON debug lines
		}
		if probe.MessageType == "summary" {
			var s BackupSummary
			if err := json.Unmarshal(line, &s); err != nil {
				return nil, fmt.Errorf("parse backup summary: %w", err)
			}
			summary = &s
		}
	}
	if summary == nil {
		return nil, errors.New("backup: no summary line in restic --json output")
	}
	return summary, nil
}

// RestoreOpts controls one `restic restore` invocation.
type RestoreOpts struct {
	SnapshotID string // ID, short_id, or "latest"
	Target     string // --target dir
	Include    []string
	Exclude    []string
}

// Restore materializes a snapshot to disk.
func (c *Client) Restore(ctx context.Context, opts RestoreOpts) error {
	if opts.SnapshotID == "" {
		return errors.New("restore: SnapshotID required")
	}
	if opts.Target == "" {
		return errors.New("restore: Target required")
	}
	args := []string{"restore", opts.SnapshotID, "--target", opts.Target}
	for _, p := range opts.Include {
		args = append(args, "--include", p)
	}
	for _, p := range opts.Exclude {
		args = append(args, "--exclude", p)
	}
	_, _, err := c.run(ctx, args, nil)
	return err
}

// Dump streams a single file's content from a snapshot to stdout. Used
// by the manifest snapshot reader and stdin-backed db dumps.
func (c *Client) Dump(ctx context.Context, snapshotID, path string) ([]byte, error) {
	out, _, err := c.run(ctx, []string{"dump", snapshotID, path}, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ForgetOpts maps to `restic forget` flags.
type ForgetOpts struct {
	KeepDaily   uint
	KeepWeekly  uint
	KeepMonthly uint
	Tags        []Tag // limit retention scope to snapshots carrying every tag
	Prune       bool  // also run prune
}

// Forget runs `restic forget` with the configured policy. Returns
// stdout for the caller to log.
func (c *Client) Forget(ctx context.Context, opts ForgetOpts) ([]byte, error) {
	args := []string{"forget"}
	if opts.KeepDaily > 0 {
		args = append(args, "--keep-daily", strconv.FormatUint(uint64(opts.KeepDaily), 10))
	}
	if opts.KeepWeekly > 0 {
		args = append(args, "--keep-weekly", strconv.FormatUint(uint64(opts.KeepWeekly), 10))
	}
	if opts.KeepMonthly > 0 {
		args = append(args, "--keep-monthly", strconv.FormatUint(uint64(opts.KeepMonthly), 10))
	}
	for _, t := range opts.Tags {
		args = append(args, "--tag", string(t))
	}
	if opts.Prune {
		args = append(args, "--prune")
	}
	out, _, err := c.run(ctx, args, nil)
	return out, err
}

// Init runs `restic init` against the configured repo. Idempotent at
// the call site: callers swallow `repository already initialized`.
func (c *Client) Init(ctx context.Context) error {
	_, _, err := c.run(ctx, []string{"init"}, nil)
	return err
}

// Check runs `restic check` to verify repo integrity. ReadDataSubset
// (e.g. "10%") triggers data-block sampling; empty checks structure only.
func (c *Client) Check(ctx context.Context, readDataSubset string) error {
	args := []string{"check"}
	if readDataSubset != "" {
		args = append(args, "--read-data-subset", readDataSubset)
	}
	_, _, err := c.run(ctx, args, nil)
	return err
}
