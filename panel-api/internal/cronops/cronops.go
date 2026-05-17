// Package cronops is the shared Cron Job Intake (ADR-0083 shape,
// ADR-0101). It owns the create/update invariant end-to-end:
// validate (name → Linux account → schedule → command) → resolve
// owned docroots → persist → synchronous Agent apply. The REST
// handler (panel-api/internal/api/cron.go) and the `jabali cron`
// cobra subcommand are thin adapters: build the input, call here,
// render/translate the typed sentinel errors in their own format.
//
// ADR-0101: apply is part of the invariant. An enabled job is
// agent-applied synchronously; on create, a failed apply rolls the
// row back (the REST contract — the CLI previously skipped apply
// entirely and leaked stuck rows to the reconciler). The reconciler
// still owns ongoing drift convergence (ADR-0004).
package cronops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/cronvalidate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Narrow seams — concrete repository.* types satisfy these
// structurally; tests supply 1-method fakes (no full-repo mock).
type AgentCaller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}
type UserFinder interface {
	FindByID(ctx context.Context, id string) (*models.User, error)
}
type DomainLister interface {
	ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.Domain, int64, error)
}
type CronStore interface {
	Create(ctx context.Context, j *models.CronJob) error
	Delete(ctx context.Context, id string) error
	FindByID(ctx context.Context, id string) (*models.CronJob, error)
	Update(ctx context.Context, j *models.CronJob) error
}

type Deps struct {
	Users    UserFinder
	Domains  DomainLister
	CronJobs CronStore
	Agent    AgentCaller
}

// CreateInput / UpdatePatch — both adapters build these from their
// own argument parsing. Authorization (who may touch the job) stays
// in the adapter; cronops owns the intake invariant only.
type CreateInput struct {
	UserID   string
	Name     string
	Schedule string
	Command  string
	Enabled  bool
}

type UpdatePatch struct {
	Name     *string
	Command  *string
	Schedule *string
	Enabled  *bool
}

// Sentinels — adapters errors.Is these to map HTTP status / CLI exit.
// cronvalidate.ErrNoLinuxAccount is propagated (wrapped) so the
// existing 409 user_has_no_linux_account mapping is preserved.
var (
	ErrDeps            = errors.New("cronops: dependencies not wired")
	ErrUserNotFound    = errors.New("cronops: user not found")
	ErrJobNotFound     = errors.New("cronops: cron job not found")
	ErrNameInvalid     = errors.New("cronops: invalid cron name")
	ErrScheduleInvalid = errors.New("cronops: invalid schedule")
	ErrCommandInvalid  = errors.New("cronops: invalid command")
	ErrAgentFailed     = errors.New("cronops: agent dispatch failed")
	ErrInternal        = errors.New("cronops: internal error")
)

const agentTimeout = 30 * time.Second

// applyParams / removeParams mirror the panel-agent cron.apply /
// cron.remove wire contract verbatim (verified against
// panel-agent/internal/commands/cron_apply.go). This is the single
// canonical copy; adapters no longer carry their own.
type applyParams struct {
	UserID        string   `json:"user_id"`
	Username      string   `json:"username"`
	JobID         string   `json:"job_id"`
	Name          string   `json:"name"`
	Command       string   `json:"command"`
	Schedule      string   `json:"schedule"`
	OwnedDocroots []string `json:"owned_docroots"`
}

type removeParams struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	JobID    string `json:"job_id"`
}

func depsWired(d Deps) bool {
	return d.Users != nil && d.Domains != nil && d.CronJobs != nil && d.Agent != nil
}

// resolveLinuxUser loads the user and enforces the Linux-account
// precondition (single rule, cronvalidate). Returns the username.
func resolveLinuxUser(ctx context.Context, d Deps, userID string) (string, error) {
	u, err := d.Users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("%w: load user: %v", ErrInternal, err)
	}
	uname := ""
	if u != nil && u.Username != nil {
		uname = *u.Username
	}
	if err := cronvalidate.ValidateLinuxAccount(uname); err != nil {
		return "", fmt.Errorf("cronops: %w", err) // preserves errors.Is(ErrNoLinuxAccount)
	}
	return uname, nil
}

func ownedDocroots(ctx context.Context, d Deps, userID string) ([]string, error) {
	domains, _, err := d.Domains.ListByUserID(ctx, userID, repository.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("%w: resolve docroots: %v", ErrInternal, err)
	}
	out := make([]string, 0, len(domains))
	for _, dm := range domains {
		if dm.DocRoot != "" {
			out = append(out, dm.DocRoot)
		}
	}
	return out, nil
}

func apply(ctx context.Context, d Deps, job *models.CronJob, username string, docroots []string) error {
	actx, cancel := context.WithTimeout(ctx, agentTimeout)
	defer cancel()
	_, err := d.Agent.Call(actx, "cron.apply", applyParams{
		UserID: job.UserID, Username: username, JobID: job.ID,
		Name: job.Name, Command: job.Command, Schedule: job.Schedule,
		OwnedDocroots: docroots,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAgentFailed, err)
	}
	return nil
}

// Create runs the full Cron Job Intake invariant. On an enabled job
// it agent-applies synchronously and rolls the row back if apply
// fails (ADR-0101).
func Create(ctx context.Context, d Deps, in CreateInput) (*models.CronJob, error) {
	if !depsWired(d) {
		return nil, ErrDeps
	}
	if err := cronvalidate.ValidateCronName(in.Name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNameInvalid, err)
	}
	username, err := resolveLinuxUser(ctx, d, in.UserID)
	if err != nil {
		return nil, err
	}
	if err := cronvalidate.ValidateSchedule(in.Schedule); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScheduleInvalid, err)
	}
	docroots, err := ownedDocroots(ctx, d, in.UserID)
	if err != nil {
		return nil, err
	}
	if _, err := cronvalidate.ValidateCommand(in.Command, docroots); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandInvalid, err)
	}

	job := &models.CronJob{
		ID:       ids.NewULID(),
		UserID:   in.UserID,
		Name:     in.Name,
		Command:  in.Command,
		Schedule: in.Schedule,
		Enabled:  in.Enabled,
	}
	if err := d.CronJobs.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("%w: persist: %v", ErrInternal, err)
	}
	if job.Enabled {
		if err := apply(ctx, d, job, username, docroots); err != nil {
			_ = d.CronJobs.Delete(ctx, job.ID) // roll back — REST contract
			return nil, err
		}
	}
	return job, nil
}

// Update applies a partial patch to an existing job, re-validates the
// changed fields, persists, then agent-applies (enabled) or
// agent-removes (disabled — best-effort; the reconciler reconciles).
// Authorization is the adapter's responsibility (REST claims / CLI
// operator); cronops only owns the intake invariant.
func Update(ctx context.Context, d Deps, jobID string, patch UpdatePatch) (*models.CronJob, error) {
	if !depsWired(d) {
		return nil, ErrDeps
	}
	job, err := d.CronJobs.FindByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("%w: load job: %v", ErrInternal, err)
	}
	username, err := resolveLinuxUser(ctx, d, job.UserID)
	if err != nil {
		return nil, err
	}
	docroots, err := ownedDocroots(ctx, d, job.UserID)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		if err := cronvalidate.ValidateCronName(*patch.Name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNameInvalid, err)
		}
		job.Name = *patch.Name
	}
	if patch.Command != nil {
		if _, err := cronvalidate.ValidateCommand(*patch.Command, docroots); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCommandInvalid, err)
		}
		job.Command = *patch.Command
	}
	if patch.Schedule != nil {
		if err := cronvalidate.ValidateSchedule(*patch.Schedule); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrScheduleInvalid, err)
		}
		job.Schedule = *patch.Schedule
	}
	if patch.Enabled != nil {
		job.Enabled = *patch.Enabled
	}
	if err := d.CronJobs.Update(ctx, job); err != nil {
		return nil, fmt.Errorf("%w: persist: %v", ErrInternal, err)
	}
	if job.Enabled {
		if err := apply(ctx, d, job, username, docroots); err != nil {
			return nil, err
		}
	} else {
		actx, cancel := context.WithTimeout(ctx, agentTimeout)
		_, _ = d.Agent.Call(actx, "cron.remove", removeParams{
			UserID: job.UserID, Username: username, JobID: job.ID,
		})
		cancel() // best-effort — reconciler converges a stale timer
	}
	return job, nil
}
