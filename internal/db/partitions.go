package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/katuva/wallet/dpk/logger"
	"gorm.io/gorm"
)

// partitionedTables are range-partitioned by created_at. A row whose month
// has no partition is rejected, so partitions are created ahead of time.
var partitionedTables = []string{"transactions", "ledgers"}

// MonthsAhead is how many future months always have a partition ready.
const MonthsAhead = 2

// partitionLockKey serialises partition creation across replicas booting at
// the same time (pg_advisory_xact_lock is released with the transaction).
const partitionLockKey = 7341920

// EnsurePartitions creates monthly partitions for the current month and the
// next MonthsAhead months. It is idempotent, safe to call repeatedly, and
// safe to call concurrently from many pods: an advisory lock makes one
// creator at a time, and "already exists" from a racing peer is ignored.
func EnsurePartitions(gdb *gorm.DB, now time.Time) error {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	return gdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", partitionLockKey).Error; err != nil {
			return fmt.Errorf("partition lock: %w", err)
		}
		for i := 0; i <= MonthsAhead; i++ {
			from := start.AddDate(0, i, 0)
			to := from.AddDate(0, 1, 0)
			for _, table := range partitionedTables {
				if err := createPartition(tx, table, from, to); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func createPartition(gdb *gorm.DB, table string, from, to time.Time) error {
	name := fmt.Sprintf("%s_%s", table, from.Format("2006_01"))
	stmt := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		name, table, from.Format("2006-01-02"), to.Format("2006-01-02"),
	)
	if err := gdb.Exec(stmt).Error; err != nil {
		if alreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create partition %s: %w", name, err)
	}
	return nil
}

// alreadyExists recognises the races IF NOT EXISTS does not cover:
// 42P07 duplicate_table, 23505 unique_violation on pg_type/pg_class.
func alreadyExists(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P07" || pgErr.Code == "23505"
	}
	return false
}

// RunPartitionMaintenance re-runs EnsurePartitions once a day until ctx ends,
// so a long-running node never reaches a month without a partition.
func RunPartitionMaintenance(ctx context.Context, gdb *gorm.DB) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := EnsurePartitions(gdb, time.Now()); err != nil {
				logger.ErrorLog.Printf("partition maintenance: %v", err)
			}
		}
	}
}
