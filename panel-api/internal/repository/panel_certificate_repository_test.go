package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestPanelCertificateRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPanelCertificateRepository(gdb)

	mock.ExpectQuery(`SELECT \* FROM .panel_certificate. WHERE .panel_certificate.\..id. = .? ORDER BY .panel_certificate.\..id. LIMIT .?`).
		WithArgs(1, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.Get(context.Background())
	require.Error(t, err)
	assert.Equal(t, repository.ErrNotFound, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPanelCertificateRepository_EnsureDefault_Creates(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPanelCertificateRepository(gdb)

	// EnsureDefault first calls Get → ErrRecordNotFound
	mock.ExpectQuery(`SELECT \* FROM .panel_certificate. WHERE .panel_certificate.\..id. = .? ORDER BY .panel_certificate.\..id. LIMIT .?`).
		WithArgs(1, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	// Then Create inserts. MariaDB dialect uses INSERT ... RETURNING id.
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO .panel_certificate.`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	row, err := repo.EnsureDefault(context.Background(), "panel.example.com")
	require.NoError(t, err)
	assert.Equal(t, uint8(1), row.ID)
	assert.Equal(t, "panel.example.com", row.Hostname)
	assert.Equal(t, models.PanelCertStatusSelfSigned, row.Status)
	assert.Equal(t, "/etc/jabali/tls/panel.crt", row.CertPEMPath)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPanelCertificateRepository_EnsureDefault_ReturnsExisting(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPanelCertificateRepository(gdb)

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{
		"id", "hostname", "status", "cert_pem_path",
		"issued_at", "expires_at", "last_error", "attempt_count",
		"next_retry_at", "staging", "use_le", "updated_at",
	}).AddRow(1, "panel.example.com", "issued", "/etc/jabali/tls/panel.crt",
		now, now.Add(90*24*time.Hour), "", 0, nil, false, true, now)

	mock.ExpectQuery(`SELECT \* FROM .panel_certificate. WHERE .panel_certificate.\..id. = .? ORDER BY .panel_certificate.\..id. LIMIT .?`).
		WithArgs(1, 1).
		WillReturnRows(rows)

	got, err := repo.EnsureDefault(context.Background(), "panel.example.com")
	require.NoError(t, err)
	assert.Equal(t, "issued", got.Status)
	assert.True(t, got.UseLE)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPanelCertificateRepository_MarkIssued(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPanelCertificateRepository(gdb)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .panel_certificate. SET .* WHERE id = ?`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	issued := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	expires := issued.Add(90 * 24 * time.Hour)
	err := repo.MarkIssued(context.Background(), issued, expires)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPanelCertificateRepository_MarkPendingRetry(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewPanelCertificateRepository(gdb)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .panel_certificate. SET .* WHERE id = ?`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.MarkPendingRetry(context.Background(), "rate limited", 3*time.Hour)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
