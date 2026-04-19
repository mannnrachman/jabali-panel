package main

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTOTPTestDB wires sqlmock into a GORM MariaDB dialect. Mirrors
// internal/repository/testutil_test.go so tests can assert on exact SQL.
func newTOTPTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, func()) {
	t.Helper()

	raw, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("10.11.6-MariaDB"))

	gdb, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      raw,
		SkipInitializeWithVersion: false,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	return gdb, mock, func() { _ = raw.Close() }
}

// withSharedDB swaps the package-level sharedDB for the test and restores it
// on cleanup. Matches how the cmd/server package already couples subcommands
// to globals (see admin_disable_2fa_test.go).
func withSharedDB(t *testing.T, gdb *gorm.DB) {
	t.Helper()
	prev := sharedDB
	sharedDB = gdb
	t.Cleanup(func() { sharedDB = prev })
}

func TestRunKratosMigrateTOTPReport_WritesCSV(t *testing.T) {
	gdb, mock, close := newTOTPTestDB(t)
	defer close()
	withSharedDB(t, gdb)

	// Two TOTP-enabled users. Alice is already linked to Kratos; Bob is not
	// (the warn branch exercises the "run password migration first" hint).
	mock.ExpectQuery(`SELECT \* FROM .*users.* WHERE totp_enabled = \? .*ORDER BY email ASC`).
		WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "email", "username", "is_admin",
			"totp_enabled", "totp_enabled_at", "kratos_identity_id",
			"created_at",
		}).
			AddRow("01ALICE", "alice@example.com", "alice", false, true, nil, "kratos-uuid-alice", nil).
			AddRow("01BOB", "bob@example.com", nil, true, true, nil, nil, nil))

	// Backup-code count query: alice has 2 unused, bob has 0 (omitted).
	mock.ExpectQuery(`SELECT user_id AS user_id, COUNT\(\*\) AS n FROM .*totp_backup_codes.* WHERE used_at IS NULL GROUP BY .*user_id`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "n"}).
			AddRow("01ALICE", 2))

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "report.csv")

	err := runKratosMigrateTOTPReport(context.Background(), kratosMigrateOptions{
		TOTPOnly:   true,
		TOTPOutput: outPath,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	r := csv.NewReader(strings.NewReader(string(data)))
	rows, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3, "expect header + 2 users")

	// Header.
	assert.Equal(t, []string{
		"email", "username", "panel_user_id", "kratos_identity_id",
		"totp_enabled_at", "unused_backup_codes", "needs_reenrollment",
	}, rows[0])

	// Alice row (linked to Kratos, 2 unused codes).
	assert.Equal(t, "alice@example.com", rows[1][0])
	assert.Equal(t, "alice", rows[1][1])
	assert.Equal(t, "01ALICE", rows[1][2])
	assert.Equal(t, "kratos-uuid-alice", rows[1][3])
	assert.Equal(t, "2", rows[1][5])
	assert.Equal(t, "yes", rows[1][6])

	// Bob row (no Kratos linkage, zero unused codes, username NULL).
	assert.Equal(t, "bob@example.com", rows[2][0])
	assert.Equal(t, "", rows[2][1], "NULL username stays empty")
	assert.Equal(t, "01BOB", rows[2][2])
	assert.Equal(t, "", rows[2][3], "NULL kratos_identity_id stays empty")
	assert.Equal(t, "0", rows[2][5])
}

func TestRunKratosMigrateTOTPReport_EmptyResult(t *testing.T) {
	gdb, mock, close := newTOTPTestDB(t)
	defer close()
	withSharedDB(t, gdb)

	mock.ExpectQuery(`SELECT \* FROM .*users.* WHERE totp_enabled = \?`).
		WithArgs(true).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}))

	mock.ExpectQuery(`SELECT user_id AS user_id, COUNT\(\*\) AS n FROM .*totp_backup_codes.*`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "n"}))

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "empty.csv")

	err := runKratosMigrateTOTPReport(context.Background(), kratosMigrateOptions{
		TOTPOnly:   true,
		TOTPOutput: outPath,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	// An empty run still writes the header — operators can grep -c and reason
	// about a zero-row outcome.
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "email,username,panel_user_id")
	assert.Equal(t, 1, strings.Count(string(data), "\n"),
		"empty report should be header-only (1 line)")
}

func TestOpenTOTPReportOutput_StdoutFallback(t *testing.T) {
	w, cleanup, err := openTOTPReportOutput("")
	require.NoError(t, err)
	assert.Equal(t, os.Stdout, w)
	cleanup() // must not close stdout

	w2, cleanup2, err := openTOTPReportOutput("-")
	require.NoError(t, err)
	assert.Equal(t, os.Stdout, w2)
	cleanup2()
}

func TestOpenTOTPReportOutput_FilePath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "out.csv")

	w, cleanup, err := openTOTPReportOutput(path)
	require.NoError(t, err)
	_, werr := w.(interface{ Write([]byte) (int, error) }).Write([]byte("hello"))
	require.NoError(t, werr)
	cleanup()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}
