// Command wallet runs the distributed wallet service.
//
//	wallet              start the HTTP + gRPC servers (migrations run first)
//	wallet migrate up   apply pending migrations and exit
//	wallet migrate down roll back the most recent migration and exit
package main

import (
	"os"
	"time"

	"github.com/katuva/wallet/cmd/servers"
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/db"
)

func main() {
	logger.StartLogger()
	cfg := config.Load()

	gdb, err := db.Connect(cfg)
	if err != nil {
		logger.ErrorLog.Fatalf("startup: %v", err)
	}

	if len(os.Args) > 2 && os.Args[1] == "migrate" {
		switch os.Args[2] {
		case "up":
			err = db.Migrate(db.DSN(cfg))
		case "down":
			err = db.Rollback(db.DSN(cfg))
		default:
			logger.ErrorLog.Fatalf("unknown migrate command %q (want up|down)", os.Args[2])
		}
		if err != nil {
			logger.ErrorLog.Fatalf("migrate: %v", err)
		}
		return
	}

	if err = db.Migrate(db.DSN(cfg)); err != nil {
		logger.ErrorLog.Fatalf("startup: %v", err)
	}
	if err = db.EnsurePartitions(gdb, time.Now()); err != nil {
		logger.ErrorLog.Fatalf("startup: %v", err)
	}

	defer cache.Start(cache.Options{
		Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB, DefaultTTL: cfg.CacheTTL,
	}).Close()

	if err = servers.Run(cfg, gdb); err != nil {
		logger.ErrorLog.Fatalf("server: %v", err)
	}
}
