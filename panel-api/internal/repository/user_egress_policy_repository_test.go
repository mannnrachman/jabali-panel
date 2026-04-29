package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// GORM-with-MariaDB renders an INSERT … RETURNING when CURRENT_TIMESTAMP
// defaults are present (updated_at, created_at). The matcher is regex so
// these expectations cover both forms.

func TestUserEgressPolicy_EnsureDefault_NoOpWhenExisting(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressPolicyRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `user_egress_policies`").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	require.NoError(t, repo.EnsureDefault(context.Background(), "01J5USER000000000000000001", ""))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserEgressPolicy_Upsert_StampsLearningStartedAt(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressPolicyRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `user_egress_policies`").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	p := &models.UserEgressPolicy{
		UserID: "01J5USER000000000000000002",
		State:  models.UserEgressStateLearning,
	}
	require.NoError(t, repo.Upsert(context.Background(), p))
	require.NotNil(t, p.LearningStartedAt, "learning state must stamp learning_started_at")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserEgressPolicy_Upsert_ClearsLearningStartedAtOnEnforce(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressPolicyRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `user_egress_policies`").
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	prev := time.Now().Add(-7 * 24 * time.Hour)
	p := &models.UserEgressPolicy{
		UserID:            "01J5USER000000000000000003",
		State:             models.UserEgressStateEnforced,
		LearningStartedAt: &prev,
	}
	require.NoError(t, repo.Upsert(context.Background(), p))
	require.Nil(t, p.LearningStartedAt, "non-learning states must clear learning_started_at")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserEgressPolicy_Get_NotFoundTranslated(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressPolicyRepository(db)

	mock.ExpectQuery("SELECT .* FROM `user_egress_policies` WHERE user_id = \\?").
		WithArgs("missing", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.Get(context.Background(), "missing")
	require.Error(t, err)
}

func TestUserEgressPolicy_ListMatureLearning_RejectsZeroAge(t *testing.T) {
	db, _, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressPolicyRepository(db)

	_, err := repo.ListMatureLearning(context.Background(), 0)
	require.Error(t, err, "zero or negative age must be rejected")
}

func TestUserEgressRequest_Create_DefaultsProtocolToTCP(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressRequestRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO `user_egress_requests`").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	mock.ExpectCommit()

	r := &models.UserEgressRequest{
		ID:     "01J5REQ0000000000000000001",
		UserID: "01J5USER000000000000000099",
		CIDR:   "203.0.113.5/32",
		Reason: "github API",
	}
	require.NoError(t, repo.Create(context.Background(), r))
	require.Equal(t, models.UserEgressProtocolTCP, r.Protocol)
	require.Equal(t, models.UserEgressRequestStatusPending, r.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserEgressRequest_CancelPending_NotFoundWhenAlreadyDecided(t *testing.T) {
	db, mock, raw := newMockBackupDB(t)
	defer raw.Close()
	repo := NewUserEgressRequestRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `user_egress_requests`").
		WithArgs("01J5REQ0000000000000000002", "01J5USER000000000000000099", "pending").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := repo.CancelPending(context.Background(), "01J5REQ0000000000000000002", "01J5USER000000000000000099")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
