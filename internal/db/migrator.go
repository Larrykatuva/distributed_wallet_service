package db

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/katuva/wallet/dpk/logger"
	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var sqlFiles embed.FS

func RunMigrations() {
	files, err := fs.Glob(sqlFiles, "migrations/*.up.sql")
	if err != nil {
		logger.ErrorLog.Fatal("migrator: failed to list sql files: ", err)
	}

	sort.Strings(files)

	logger.InfoLog.Printf("migrator: found %d migration file(s)", len(files))

	for _, file := range files {
		name := filepath.Base(file)

		content, err := sqlFiles.ReadFile(file)
		if err != nil {
			logger.ErrorLog.Fatalf("migrator: failed to read %s: %v", name, err)
		}

		if err = runInTransaction(name, string(content)); err != nil {
			logger.ErrorLog.Fatalf("migrator: failed to run %s: %v", name, err)
		}

		logger.InfoLog.Printf("migrator: applied %s", name)
	}

	logger.InfoLog.Println("migrator: all migrations applied successfully")
}

func runInTransaction(name, content string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(content).Error; err != nil {
			return fmt.Errorf("exec %s: %w", name, err)
		}
		return nil
	})
}
