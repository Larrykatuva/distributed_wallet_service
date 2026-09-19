package db

import (
	"fmt"
	"time"

	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// DSN is the direct Postgres connection string. Use it for anything that
// needs a real session: migrations (advisory locks), one-off admin commands.
func DSN(cfg *config.Config) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DbHost, cfg.DbPort, cfg.DbUser, cfg.DbPassword, cfg.DbName, cfg.DbSSLMode)
}

// AppDSN is what the application pool connects to: PgBouncer when enabled,
// otherwise Postgres directly. In transaction pooling mode a server
// connection is only borrowed per transaction, so named prepared statements
// cannot be relied on; pgx is switched to cache_describe (unnamed statements,
// still binary and still cached client-side) and GORM statement caching is off.
func AppDSN(cfg *config.Config) (dsn string, prepared bool) {
	if !cfg.UsePgBouncer {
		return DSN(cfg), true
	}
	dsn = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.PgBouncerHost, cfg.PgBouncerPort, cfg.DbUser, cfg.DbPassword, cfg.DbName, cfg.DbSSLMode)
	if cfg.PgBouncerPoolMode == "session" {
		return dsn, true
	}
	return dsn + " default_query_exec_mode=cache_describe", false
}

// Connect opens the application pool and verifies it with a ping.
// Callers should treat an error as fatal: nothing works without the database.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	dsn, prepared := AppDSN(cfg)
	gdb, err := Open(dsn, prepared)
	if err != nil {
		return nil, err
	}
	if cfg.UsePgBouncer {
		logger.InfoLog.Printf("database: pooled via PgBouncer at %s:%s (%s mode, prepared statements %v)",
			cfg.PgBouncerHost, cfg.PgBouncerPort, cfg.PgBouncerPoolMode, prepared)
	} else {
		logger.InfoLog.Printf("database: direct connection to %s:%s", cfg.DbHost, cfg.DbPort)
	}
	return gdb, nil
}

// Open connects to the given DSN with the pool settings the service expects.
// prepared enables GORM's prepared-statement cache; disable it behind a
// transaction-mode pooler.
func Open(dsn string, prepared bool) (*gorm.DB, error) {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
		Logger:         gormlogger.Default.LogMode(gormlogger.Error),
		// Cache prepared statements per connection: the hot path runs the
		// same handful of statements millions of times.
		PrepareStmt: prepared,
		// Single-statement writes need no wrapping transaction; the paths
		// that must be atomic (balance + ledger) open one explicitly.
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("unwrap sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err = sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	logger.InfoLog.Println("database connection established")
	return gdb, nil
}
