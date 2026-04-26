package reconciler

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ReconcileSSHKeysForUser syncs the user's SSH keys to authorized_keys.
// Skips silently if user has no Linux username (admin-only or pending).
// Adds user to jabali-sftp group, then writes or deletes authorized_keys.
func (r *Reconciler) ReconcileSSHKeysForUser(ctx context.Context, userID string) error {
	// Fetch user to check for Linux username
	user, err := r.users.FindByID(ctx, userID)
	if err != nil {
		if err == repository.ErrNotFound {
			// User doesn't exist; skip silently
			return nil
		}
		return fmt.Errorf("fetch user: %w", err)
	}

	// Skip if user has no Linux username
	if user.Username == nil || *user.Username == "" {
		r.log.DebugContext(ctx, "reconcile ssh keys: skip (no username)", "user_id", userID)
		return nil
	}

	// Group membership gates SSH access mode:
	//   ssh_enabled=true  → leave jabali-sftp group → real shell login
	//   ssh_enabled=false → join jabali-sftp group  → SFTP-only (Match block)
	// SSHEnabled lives on the hosting package, not the per-user overrides
	// table. Missing package (no package_id, or package fetch fails)
	// keeps the safe default (SFTP-only).
	sshEnabled := false
	if r.packages != nil && user.PackageID != nil && *user.PackageID != "" {
		pkg, pkgErr := r.packages.FindByID(ctx, *user.PackageID)
		if pkgErr == nil && pkg != nil {
			sshEnabled = pkg.SSHEnabled
		}
	}
	groupMethod := "ssh.user.join_sftp_group"
	if sshEnabled {
		groupMethod = "ssh.user.leave_sftp_group"
	}
	if _, err := r.agent.Call(ctx, groupMethod, map[string]interface{}{
		"username": *user.Username,
	}); err != nil {
		return fmt.Errorf("%s: %w", groupMethod, err)
	}

	// Fetch user's SSH keys
	keys, err := r.sshKeys.ListByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("list ssh keys: %w", err)
	}

	if len(keys) > 0 {
		// Write authorized_keys file
		lines := make([]string, len(keys))
		for i, key := range keys {
			lines[i] = key.PublicKey
		}
		if _, err := r.agent.Call(ctx, "ssh.authorized_keys.write", map[string]interface{}{
			"username": *user.Username,
			"keys":     lines,
		}); err != nil {
			return fmt.Errorf("write authorized_keys: %w", err)
		}
		r.log.InfoContext(ctx, "reconcile ssh keys: wrote authorized_keys",
			"user_id", userID, "username", *user.Username, "key_count", len(keys))
	} else {
		// Delete authorized_keys file (user has no keys)
		if _, err := r.agent.Call(ctx, "ssh.authorized_keys.delete", map[string]interface{}{
			"username": *user.Username,
		}); err != nil {
			return fmt.Errorf("delete authorized_keys: %w", err)
		}
		r.log.InfoContext(ctx, "reconcile ssh keys: deleted authorized_keys",
			"user_id", userID, "username", *user.Username)
	}

	return nil
}

// reconcileSSHKeysForAllUsers iterates all users with a username and reconciles their SSH keys.
func (r *Reconciler) reconcileSSHKeysForAllUsers(ctx context.Context) {
	// Skip if SSH keys repository is not initialized
	if r.sshKeys == nil {
		return
	}

	users, _, err := r.users.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		r.log.WarnContext(ctx, "reconcile ssh keys for all users: list users", "error", err)
		return
	}

	for i := range users {
		user := &users[i]
		if user.Username == nil || *user.Username == "" {
			continue // Skip users without a Linux username
		}

		userCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := r.ReconcileSSHKeysForUser(userCtx, user.ID)
		cancel()

		if err != nil {
			r.log.WarnContext(ctx, "reconcile ssh keys: per-user error",
				"user_id", user.ID, "username", *user.Username, "error", err)
		}
	}
}
