package db

import (
	"database/sql"
	"fmt"
	"strings"

	// register the mysql driver with database/sql
	_ "github.com/go-sql-driver/mysql"
)

// openRawSQL returns a *sql.DB for golang-migrate, which needs its own
// connection independent of GORM's. Caller closes.
//
// `multiStatements=true` is forced on so migration files can contain more
// than one ';'-separated statement. Without it MariaDB's parser rejects
// the second statement. golang-migrate's mysql driver uses whatever
// sql.DB we hand it, so it inherits this behaviour transparently.
func openRawSQL(driverDSN string) (*sql.DB, error) {
	sqlDB, err := sql.Open("mysql", withMultiStatements(driverDSN))
	if err != nil {
		return nil, fmt.Errorf("sql.Open mysql: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return sqlDB, nil
}

// withMultiStatements appends `multiStatements=true` to the DSN query
// string if it isn't already set. Idempotent: safe to call on a DSN that
// already has it.
func withMultiStatements(dsn string) string {
	if strings.Contains(dsn, "multiStatements=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "multiStatements=true"
}
