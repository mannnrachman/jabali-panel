package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestBackupDestination_Create_StampsTimestamps(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBackupDestinationRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `backup_destinations`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	d := &models.BackupDestination{
		ID:      "01J5DEST00000000000000000A",
		Name:    "offsite-s3",
		Kind:    models.BackupDestinationKindS3,
		URL:     "s3:s3.amazonaws.com/jabali-backups",
		Enabled: true,
	}
	require.NoError(t, repo.Create(context.Background(), d))
	require.False(t, d.CreatedAt.IsZero())
	require.False(t, d.UpdatedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackupDestination_Get_NotFoundTranslated(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBackupDestinationRepository(db)

	mock.ExpectQuery("SELECT .* FROM `backup_destinations` WHERE id = \\?").
		WithArgs("missing", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.Get(context.Background(), "missing")
	require.True(t, errors.Is(err, ErrNotFound))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackupDestination_ListEnabled_FiltersOnFlag(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewBackupDestinationRepository(db)

	mock.ExpectQuery("SELECT \\* FROM `backup_destinations` WHERE enabled = \\? ORDER BY name ASC").
		WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "kind", "url", "credentials_ref", "enabled",
			"created_at", "updated_at",
		}))

	rows, err := repo.ListEnabled(context.Background())
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
