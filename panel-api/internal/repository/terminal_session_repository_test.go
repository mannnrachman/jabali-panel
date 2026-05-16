package repository_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// TestTerminalConsumeValid_SingleUsePredicate is the security
// regression lock for M45: the token consume MUST be an UPDATE whose
// WHERE clause still gates on `used_at IS NULL` AND `expires_at`. If a
// future refactor drops either predicate the token stops being
// one-shot / time-bound — a leaked token becomes replayable into a
// root shell. The regexp expectations fail loudly if the guard moves.
func TestTerminalConsumeValid_SingleUsePredicate(t *testing.T) {
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := repository.NewTerminalSessionRepository(gdb)

	// Already-used or expired token: the gated UPDATE affects 0 rows.
	// Repo must return ErrNotFound and must NOT fall through to a SELECT.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .*terminal_sessions.* SET .*used_at.* WHERE .*token.* AND .*used_at.* IS NULL AND .*expires_at`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	_, err := repo.ConsumeValid(context.Background(), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef000")
	require.True(t, errors.Is(err, repository.ErrNotFound),
		"used/expired token must yield ErrNotFound, got %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTerminalConsumeValid_HappyPath — a fresh token: UPDATE marks one
// row, then the row is read back for the caller's IP/uid re-bind check.
func TestTerminalConsumeValid_HappyPath(t *testing.T) {
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()
	repo := repository.NewTerminalSessionRepository(gdb)

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .*terminal_sessions.* SET .*used_at`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT")).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "token", "client_ip", "expires_at",
			"used_at", "started_at", "ended_at", "cast_path", "created_at",
		}).AddRow(
			"01SESSIONULID00000000000000", "01ADMINULID0000000000000000",
			"tok", "203.0.113.9", now.Add(time.Minute),
			now, nil, nil, "", now,
		))
	mock.ExpectCommit()

	s, err := repo.ConsumeValid(context.Background(), "tok")
	require.NoError(t, err)
	require.Equal(t, "01ADMINULID0000000000000000", s.UserID)
	require.Equal(t, "203.0.113.9", s.ClientIP)
	require.NoError(t, mock.ExpectationsWereMet())
}
