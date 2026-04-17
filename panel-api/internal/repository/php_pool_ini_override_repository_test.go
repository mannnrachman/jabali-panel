package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// TestPHPPoolIniOverrideRepository_Create verifies ini override creation
func TestPHPPoolIniOverrideRepository_Create(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	override := &models.PHPPoolIniOverride{
		ID:        "override_123",
		PoolID:    "pool_abc",
		Directive: "upload_max_filesize",
		Value:     "256M",
		Kind:      "value",
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `php_pool_ini_overrides`")).
		WithArgs(
			override.ID,
			override.PoolID,
			override.Directive,
			override.Value,
			override.Kind,
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), override)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPHPPoolIniOverrideRepository_Create_DuplicateDirective verifies
// unique constraint violation when creating duplicate directive
func TestPHPPoolIniOverrideRepository_Create_DuplicateDirective(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	override := &models.PHPPoolIniOverride{
		ID:        "override_456",
		PoolID:    "pool_abc",
		Directive: "upload_max_filesize",
		Value:     "512M",
		Kind:      "value",
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `php_pool_ini_overrides`")).
		WithArgs(
			override.ID,
			override.PoolID,
			override.Directive,
			override.Value,
			override.Kind,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectRollback()

	err := repo.Create(context.Background(), override)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPHPPoolIniOverrideRepository_ListByPool verifies listing overrides by pool
func TestPHPPoolIniOverrideRepository_ListByPool(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows(
		[]string{"id", "pool_id", "directive", "value", "kind", "created_at", "updated_at"},
	).
		AddRow("override_1", "pool_abc", "upload_max_filesize", "256M", "value", now, now).
		AddRow("override_2", "pool_abc", "max_execution_time", "30", "value", now.Add(time.Second), now.Add(time.Second))

	mock.ExpectQuery("SELECT .* FROM `php_pool_ini_overrides` WHERE pool_id = \\?.*ORDER BY created_at ASC").
		WithArgs("pool_abc").
		WillReturnRows(rows)

	overrides, err := repo.ListByPool(context.Background(), "pool_abc")
	require.NoError(t, err)
	require.Len(t, overrides, 2)
	require.Equal(t, "override_1", overrides[0].ID)
	require.Equal(t, "override_2", overrides[1].ID)
	// Verify ordering by created_at ASC
	require.True(t, overrides[0].CreatedAt.Before(overrides[1].CreatedAt) || overrides[0].CreatedAt.Equal(overrides[1].CreatedAt))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPHPPoolIniOverrideRepository_ListByPool_Empty verifies empty result
func TestPHPPoolIniOverrideRepository_ListByPool_Empty(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	mock.ExpectQuery("SELECT .* FROM `php_pool_ini_overrides` WHERE pool_id = \\?.*ORDER BY created_at ASC").
		WithArgs("pool_empty").
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "pool_id", "directive", "value", "kind", "created_at", "updated_at"},
		))

	overrides, err := repo.ListByPool(context.Background(), "pool_empty")
	require.NoError(t, err)
	require.Empty(t, overrides)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPHPPoolIniOverrideRepository_Delete verifies override deletion
func TestPHPPoolIniOverrideRepository_Delete(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `php_pool_ini_overrides` WHERE id = ?")).
		WithArgs("override_123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), "override_123")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPHPPoolIniOverrideRepository_Delete_NotFound verifies deletion of non-existent override
func TestPHPPoolIniOverrideRepository_Delete_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewPHPPoolIniOverrideRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `php_pool_ini_overrides` WHERE id = ?")).
		WithArgs("override_nonexistent").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	// GORM's Delete does not error on 0 rows affected; it's a no-op success
	err := repo.Delete(context.Background(), "override_nonexistent")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
