package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestSSHKeyCreate_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)
	now := time.Now()

	key := &models.SSHKey{
		ID:          "sshk_abc123",
		UserID:      "user1",
		Name:        "my-key",
		PublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG...",
		Fingerprint: "SHA256:abcdef1234567890abcdef1234567890abcdef1234=",
		CreatedAt:   now,
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `ssh_keys`").
		WithArgs(
			key.ID, key.UserID, key.Name, key.PublicKey, key.Fingerprint,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), key)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyCreate_DuplicateUserFingerprint(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)
	now := time.Now()

	key := &models.SSHKey{
		ID:          "sshk_abc123",
		UserID:      "user1",
		Name:        "my-key",
		PublicKey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG...",
		Fingerprint: "SHA256:abcdef1234567890abcdef1234567890abcdef1234=",
		CreatedAt:   now,
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `ssh_keys`").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrNoRows) // Simulates unique constraint violation
	mock.ExpectRollback()

	err := repo.Create(context.Background(), key)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyFindByID_Found(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)
	now := time.Now()

	mock.ExpectQuery("SELECT .* FROM `ssh_keys` WHERE id = \\?.*LIMIT").
		WithArgs("sshk_abc123", 1).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "public_key", "fingerprint", "created_at"},
		).AddRow(
			"sshk_abc123", "user1", "my-key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG...",
			"SHA256:abcdef1234567890abcdef1234567890abcdef1234=", now,
		))

	key, err := repo.FindByID(context.Background(), "sshk_abc123")
	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, "sshk_abc123", key.ID)
	require.Equal(t, "user1", key.UserID)
	require.Equal(t, "my-key", key.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyFindByID_NotFound(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)

	mock.ExpectQuery("SELECT .* FROM `ssh_keys` WHERE id = \\?.*LIMIT").
		WithArgs("sshk_nonexistent", 1).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "public_key", "fingerprint", "created_at"},
		))

	key, err := repo.FindByID(context.Background(), "sshk_nonexistent")
	require.Error(t, err)
	require.Nil(t, key)
	require.Equal(t, ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyFindByIDAndUserID_Found(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)
	now := time.Now()

	mock.ExpectQuery("SELECT .* FROM `ssh_keys` WHERE id = \\? AND user_id = \\?.*LIMIT").
		WithArgs("sshk_abc123", "user1", 1).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "public_key", "fingerprint", "created_at"},
		).AddRow(
			"sshk_abc123", "user1", "my-key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG...",
			"SHA256:abcdef1234567890abcdef1234567890abcdef1234=", now,
		))

	key, err := repo.FindByIDAndUserID(context.Background(), "sshk_abc123", "user1")
	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, "sshk_abc123", key.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyListByUserID_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)
	now := time.Now()

	mock.ExpectQuery("SELECT .* FROM `ssh_keys` WHERE user_id = \\?.*ORDER BY").
		WithArgs("user1").
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "user_id", "name", "public_key", "fingerprint", "created_at"},
		).AddRow(
			"sshk_abc123", "user1", "key1", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG...",
			"SHA256:abcdef1234567890abcdef1234567890abcdef1234=", now,
		).AddRow(
			"sshk_xyz789", "user1", "key2", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAA...",
			"SHA256:xyz789abcdef1234567890xyz789abcdef1234567=", now,
		))

	keys, err := repo.ListByUserID(context.Background(), "user1")
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, "sshk_abc123", keys[0].ID)
	require.Equal(t, "sshk_xyz789", keys[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyDelete_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `ssh_keys` WHERE id = \\?").
		WithArgs("sshk_abc123").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), "sshk_abc123")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyDeleteByUserID_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `ssh_keys` WHERE user_id = \\?").
		WithArgs("user1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	err := repo.DeleteByUserID(context.Background(), "user1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSHKeyCountByUserID_Success(t *testing.T) {
	db, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := NewSSHKeyRepository(db)

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `ssh_keys` WHERE user_id = \\?").
		WithArgs("user1").
		WillReturnRows(sqlmock.NewRows([]string{"count(*)"}).AddRow(3))

	count, err := repo.CountByUserID(context.Background(), "user1")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
	require.NoError(t, mock.ExpectationsWereMet())
}
