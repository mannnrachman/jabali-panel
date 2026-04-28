package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestBackupCopyJob_Create_StampsDefaults(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBackupCopyJobRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `backup_copy_jobs`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	j := &models.BackupCopyJob{
		ID:            "01J5COPY00000000000000000A",
		BackupJobID:   "01J5BACKUP00000000000000A",
		DestinationID: "01J5DEST00000000000000000A",
	}
	require.NoError(t, repo.Create(context.Background(), j))
	require.Equal(t, models.BackupCopyJobStatusQueued, j.Status)
	require.NotNil(t, j.NextAttemptAt)
	require.False(t, j.CreatedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackupCopyJob_ListQueued_FiltersByStatusAndAttempt(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBackupCopyJobRepository(db)

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT \\* FROM `backup_copy_jobs` WHERE status = \\? AND \\(next_attempt_at IS NULL OR next_attempt_at <= \\?\\) ORDER BY next_attempt_at ASC, created_at ASC LIMIT \\?").
		WithArgs(models.BackupCopyJobStatusQueued, now, 5).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "backup_job_id", "destination_id", "status",
			"systemd_unit", "retry_count", "next_attempt_at",
			"started_at", "finished_at", "bytes_copied", "error_text",
			"created_at", "updated_at",
		}))

	rows, err := repo.ListQueued(context.Background(), now, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
