package db

import (
	"database/sql"
	"fmt"

	// register the mysql driver with database/sql
	_ "github.com/go-sql-driver/mysql"
)

// openRawSQL returns a *sql.DB for golang-migrate, which needs its own
// connection independent of GORM's. Caller closes.
func openRawSQL(driverDSN string) (*sql.DB, error) {
	sqlDB, err := sql.Open("mysql", driverDSN)
	if err != nil {
		return nil, fmt.Errorf("sql.Open mysql: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return sqlDB, nil
}
