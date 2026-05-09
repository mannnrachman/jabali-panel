package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Reuses newMockBackupDB from backup_job_repository_test.go (same
// package — _test.go helpers are visible across files).

func TestAutomationToken_Create_StampsCreatedAt(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `automation_tokens`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tok := &models.AutomationToken{
		ID:        "01TESTTOKEN0000000000000000",
		Name:      "monitoring-bot",
		Scopes:    models.AutomationScopes{"read:status"},
		SecretEnc: []byte{0x01, 0x02},
	}
	require.NoError(t, repo.Create(context.Background(), tok))
	require.False(t, tok.CreatedAt.IsZero(), "Create must stamp CreatedAt when zero")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutomationToken_Create_PreservesPresetCreatedAt(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `automation_tokens`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	preset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tok := &models.AutomationToken{
		ID:        "01TESTTOKEN0000000000000000",
		Name:      "preset-ts",
		Scopes:    models.AutomationScopes{"read:*"},
		SecretEnc: []byte{0x01},
		CreatedAt: preset,
	}
	require.NoError(t, repo.Create(context.Background(), tok))
	require.True(t, tok.CreatedAt.Equal(preset), "Create must NOT overwrite a non-zero CreatedAt")
}

func TestAutomationToken_FindByID_NotFoundTranslated(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectQuery("FROM `automation_tokens`").
		WillReturnError(gorm.ErrRecordNotFound)

	tok, err := repo.FindByID(context.Background(), "01MISSINGTOKEN000000000000")
	require.Nil(t, tok)
	require.True(t, errors.Is(err, ErrNotFound), "missing row must surface as repository.ErrNotFound")
}

func TestAutomationToken_FindByID_Hit(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectQuery("FROM `automation_tokens`").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "scopes_json", "secret_enc",
			"created_by", "created_at", "last_used_at", "last_used_ip", "revoked_at",
		}).AddRow(
			"01TESTTOKEN0000000000000000",
			"monitoring",
			`["read:*"]`,
			[]byte{0x01, 0x02},
			nil,
			time.Now(),
			nil, nil, nil,
		))

	tok, err := repo.FindByID(context.Background(), "01TESTTOKEN0000000000000000")
	require.NoError(t, err)
	require.NotNil(t, tok)
	require.Equal(t, "monitoring", tok.Name)
	require.Equal(t, models.AutomationScopes{"read:*"}, tok.Scopes)
}

func TestAutomationToken_List_OrdersByCreatedAtDesc(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectQuery("FROM `automation_tokens`.*ORDER BY created_at DESC").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "scopes_json", "secret_enc",
			"created_by", "created_at", "last_used_at", "last_used_ip", "revoked_at",
		}).AddRow(
			"01NEW000000000000000000000",
			"newer", `["read:status"]`, []byte{}, nil, time.Now(),
			nil, nil, nil,
		).AddRow(
			"01OLD000000000000000000000",
			"older", `["read:domains"]`, []byte{}, nil, time.Now().Add(-24*time.Hour),
			nil, nil, nil,
		))

	rows, err := repo.List(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "newer", rows[0].Name)
	require.Equal(t, "older", rows[1].Name)
}

func TestAutomationToken_Revoke_OnlyMutatesActiveRows(t *testing.T) {
	// Revoke uses `WHERE id = ? AND revoked_at IS NULL` so calling
	// Revoke on an already-revoked token is a no-op (audit trail
	// preservation — we don't bump revoked_at twice).
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `automation_tokens`.+revoked_at IS NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Revoke(context.Background(), "01TESTTOKEN0000000000000000"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutomationToken_BumpLastUsed_WritesBoth(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewAutomationTokenRepository(db)

	// Updates returns a single UPDATE setting both last_used_at + last_used_ip.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `automation_tokens`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.BumpLastUsed(context.Background(), "01TESTTOKEN0000000000000000", "192.0.2.1"))
	require.NoError(t, mock.ExpectationsWereMet())
}
