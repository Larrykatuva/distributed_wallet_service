package main

import (
	"github.com/katuva/wallet/cmd/servers"
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/db"
)

func init() {
	logger.StartLogger()
	
	cfg := config.Load()

	db.NewDatabase(cfg).Connect()
	db.RunMigrations()
}

func main() {
	servers.StartServer()
}
