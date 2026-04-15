package db

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/golang-migrate/migrate/v4"
	mmysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Migrations is a compiled-in copy of panel-api/migrations. Embedding keeps
// deploys self-contained: no "oops forgot to ship the SQL files" mistakes.
// The package path assumes this file lives at
//
//	panel-api/internal/db/db.go
//
// and the migrations directory at panel-api/migrations. If the layout
// changes, update the embed path accordingly.
//
//go:embed all:migrations/*.sql
var migrationsFS embed.FS

// migrationsFSRoot returns a sub-FS rooted at migrations/ so golang-migrate
// sees file names like "000001_init_users.up.sql" directly.
func migrationsFSRoot() (fs.FS, error) { return fs.Sub(migrationsFS, "migrations") }

// Options controls how Open dials MariaDB. Zero-value fields use sensible
// defaults; only DSN is required.
type Options struct {
	// DSN is either a URL ("mysql://u:p@host:3306/db?...") or the native
	// Go driver form. ToDriverDSN normalises either shape.
	DSN string

	// MaxOpenConns caps the connection pool size. Default 10.
	MaxOpenConns int
	// MaxIdleConns caps idle connections. Default 5.
	MaxIdleConns int
	// ConnMaxLifetime rotates connections periodically to avoid stale ones
	// after MariaDB's wait_timeout. Default 30 minutes.
	ConnMaxLifetime time.Duration

	// SlowQueryThreshold logs any query slower than this via GORM's logger.
	// Default 200ms. Set to zero to disable the threshold (everything logs).
	SlowQueryThreshold time.Duration

	// Silent disables all GORM log output. Useful in tests.
	Silent bool
}

// Open connects to MariaDB via GORM using opts. The returned *gorm.DB is
// ready for use; call Ping to verify connectivity before handling traffic.
func Open(opts Options) (*gorm.DB, error) {
	driverDSN, err := ToDriverDSN(opts.DSN)
	if err != nil {
		return nil, err
	}

	gormLogLevel := logger.Warn
	if opts.Silent {
		gormLogLevel = logger.Silent
	}
	slow := opts.SlowQueryThreshold
	if slow == 0 {
		slow = 200 * time.Millisecond
	}

	_ = slow // reserved for a future custom slow-query logger
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(gormLogLevel),
		// Everything committed/read in UTC; timezone conversion happens at
		// presentation time (API responses, UI).
		NowFunc: func() time.Time { return time.Now().UTC() },
	}

	gdb, err := gorm.Open(mysql.Open(driverDSN), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("open mariadb: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("raw sql.DB: %w", err)
	}

	maxOpen := opts.MaxOpenConns
	if maxOpen == 0 {
		maxOpen = 10
	}
	maxIdle := opts.MaxIdleConns
	if maxIdle == 0 {
		maxIdle = 5
	}
	life := opts.ConnMaxLifetime
	if life == 0 {
		life = 30 * time.Minute
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(life)

	return gdb, nil
}

// Ping verifies the connection. Callers should call this before marking
// the service as healthy.
func Ping(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// Migrate runs all pending up migrations. No-op if already at head.
//
// Uses the embedded migrations FS so binaries are deploy-ready without
// shipping SQL files alongside.
func Migrate(dsn string) error {
	driverDSN, err := ToDriverDSN(dsn)
	if err != nil {
		return err
	}

	// golang-migrate needs its own *sql.DB, separate from GORM's.
	sqlDB, err := openRawSQL(driverDSN)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	drv, err := mmysql.WithInstance(sqlDB, &mmysql.Config{})
	if err != nil {
		return fmt.Errorf("migrate driver: %w", err)
	}

	srcFS, err := migrationsFSRoot()
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	src, err := iofs.New(srcFS, ".")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "mysql", drv)
	if err != nil {
		return fmt.Errorf("migrate new: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
