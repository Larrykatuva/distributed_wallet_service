package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/katuva/wallet/dpk/logger"
)

//go:embed migrations/*.sql
var sqlFiles embed.FS

// Migrate applies every pending .up.sql once, tracked in schema_migrations.
// It opens its own connection because golang-migrate closes the handle it is
// given, which must never happen to the application pool.
//
// The original migrations are idempotent (IF NOT EXISTS), so adopting version
// tracking on a database migrated by the old runner is safe.
func Migrate(dsn string) error {
	m, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer m.Close()

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	logger.InfoLog.Printf("migrator: schema at version %d (dirty=%v)", version, dirty)
	return nil
}

// Rollback undoes the most recent migration. Exposed for `wallet migrate down`.
func Rollback(dsn string) error {
	m, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer m.Close()

	if err = m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

func newMigrator(dsn string) (*migrate.Migrate, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("migrator connect: %w", err)
	}

	source, err := iofs.New(sqlFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("migration source: %w", err)
	}

	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		return nil, fmt.Errorf("migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return nil, fmt.Errorf("migrator: %w", err)
	}
	return m, nil
}
