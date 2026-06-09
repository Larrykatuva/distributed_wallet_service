package servers

import (
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/internal/cluster"
	"github.com/katuva/wallet/internal/db"
)

func StartServer() {
	cfg := config.Load()

	cluster.Init(cfg)
	cluster.Start()
	defer cluster.Shutdown()

	cluster.RegisterKinds(db.DB)

	go StartGrpcServer(cfg, db.DB, cluster.Get())

	StartHttpServer(cfg, db.DB, cluster.Get())
}
