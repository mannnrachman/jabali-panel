package repository_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestSSLCertificateRepository_Create(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	now := time.Now()
	cert := &models.SSLCertificate{
		ID:       "01ARWX4FRYXZ73AK7EQQ69G5NV",
		DomainID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Status:   models.SSLStatusPending,
		Staging:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `ssl_certificates`")).
		WithArgs(
			cert.ID,
			cert.DomainID,
			models.SSLStatusPending,
			nil,            // issued_at
			nil,            // expires_at
			0,              // renewal_count
			nil,            // last_renewed_at
			nil,            // last_error
			false,          // staging
			nil,            // cert_path
			nil,            // key_path
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, cert)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_FindByDomainID_Success(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	domainID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	now := time.Now()

	// GORM's First() adds ORDER BY and LIMIT automatically
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `ssl_certificates` WHERE domain_id = ? ORDER BY `ssl_certificates`.`id` LIMIT ?")).
		WithArgs(domainID, 1).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"id", "domain_id", "status", "issued_at", "expires_at",
				"renewal_count", "last_renewed_at", "last_error", "staging",
				"cert_path", "key_path", "created_at", "updated_at",
			}).AddRow(
				"01ARWX4FRYXZ73AK7EQQ69G5NV",
				domainID,
				models.SSLStatusIssued,
				now.Add(-30*24*time.Hour),
				now.Add(60*24*time.Hour),
				0,
				nil,
				nil,
				false,
				"/etc/letsencrypt/live/example.com/fullchain.pem",
				"/etc/letsencrypt/live/example.com/privkey.pem",
				now,
				now,
			),
		)

	cert, err := repo.FindByDomainID(ctx, domainID)
	require.NoError(t, err)
	assert.NotNil(t, cert)
	assert.Equal(t, models.SSLStatusIssued, cert.Status)
	assert.Equal(t, domainID, cert.DomainID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_FindByDomainID_NotFound(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	domainID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	// GORM's First() adds ORDER BY and LIMIT automatically
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `ssl_certificates` WHERE domain_id = ? ORDER BY `ssl_certificates`.`id` LIMIT ?")).
		WithArgs(domainID, 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "domain_id", "status", "issued_at", "expires_at",
			"renewal_count", "last_renewed_at", "last_error", "staging",
			"cert_path", "key_path", "created_at", "updated_at",
		}))

	cert, err := repo.FindByDomainID(ctx, domainID)
	require.Error(t, err)
	assert.Equal(t, repository.ErrNotFound, err)
	assert.Nil(t, cert)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_UpdateStatus(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	certID := "01ARWX4FRYXZ73AK7EQQ69G5NV"
	errorMsg := "rate limited"

	mock.ExpectBegin()
	// GORM generates UPDATE with SET columns in non-deterministic order (map iteration)
	// Use AnyArg for all value args to avoid flakiness
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `ssl_certificates` SET")).
		WithArgs(
			sqlmock.AnyArg(), // status or last_error
			sqlmock.AnyArg(), // last_error or status
			sqlmock.AnyArg(), // updated_at
			certID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateStatus(ctx, certID, models.SSLStatusFailed, &errorMsg)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_UpdateAfterIssuance(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	certID := "01ARWX4FRYXZ73AK7EQQ69G5NV"
	issuedAt := time.Now()
	expiresAt := issuedAt.Add(90 * 24 * time.Hour)
	certPath := "/etc/letsencrypt/live/example.com/fullchain.pem"
	keyPath := "/etc/letsencrypt/live/example.com/privkey.pem"

	mock.ExpectBegin()
	// GORM generates UPDATE with SET columns in non-deterministic order (map iteration)
	// Use AnyArg for the UPDATE values; only check the WHERE clause argument (certID)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `ssl_certificates` SET")).
		WithArgs(
			sqlmock.AnyArg(), // cert_path
			sqlmock.AnyArg(), // expires_at
			sqlmock.AnyArg(), // issued_at
			sqlmock.AnyArg(), // key_path
			sqlmock.AnyArg(), // status
			sqlmock.AnyArg(), // updated_at
			certID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateAfterIssuance(ctx, certID, issuedAt, expiresAt, certPath, keyPath)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_DeleteByDomainID(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	domainID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM `ssl_certificates` WHERE domain_id = ?")).
		WithArgs(domainID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.DeleteByDomainID(ctx, domainID)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSSLCertificateRepository_ListDueForRenewal(t *testing.T) {
	t.Parallel()
	gdb, mock, raw := newMockDB(t)
	defer raw.Close()

	repo := repository.NewSSLCertificateRepository(gdb)
	ctx := context.Background()

	now := time.Now()
	window := 30 * 24 * time.Hour

	// Expect a query that checks status='issued' and expires_at < NOW()+30days
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `ssl_certificates` WHERE status = ? AND expires_at IS NOT NULL AND expires_at < ?")).
		WithArgs(
			models.SSLStatusIssued,
			sqlmock.AnyArg(), // deadline
		).
		WillReturnRows(
			sqlmock.NewRows([]string{
				"id", "domain_id", "status", "issued_at", "expires_at",
				"renewal_count", "last_renewed_at", "last_error", "staging",
				"cert_path", "key_path", "created_at", "updated_at",
			}).AddRow(
				"01ARWX4FRYXZ73AK7EQQ69G5NV",
				"01ARZ3NDEKTSV4RRFFQ69G5FAV",
				models.SSLStatusIssued,
				now.Add(-60*24*time.Hour),
				now.Add(15*24*time.Hour), // expires in 15 days (within 30-day window)
				0,
				nil,
				nil,
				false,
				"/etc/letsencrypt/live/example.com/fullchain.pem",
				"/etc/letsencrypt/live/example.com/privkey.pem",
				now,
				now,
			),
		)

	certs, err := repo.ListDueForRenewal(ctx, window)
	require.NoError(t, err)
	assert.Len(t, certs, 1)
	assert.Equal(t, models.SSLStatusIssued, certs[0].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}
