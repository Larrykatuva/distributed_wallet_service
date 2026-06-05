package routers

import (
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/go-chi/chi/v5"
	"github.com/katuva/wallet/internal/handlers"
	"gorm.io/gorm"
)

func Router(db *gorm.DB, cluster *cluster.Cluster) *chi.Mux {
	router := chi.NewRouter()

	transHandler := handlers.NewTransactionHandler(db, cluster)

	profileHandler := handlers.NewProfileHandler(db)

	walletHandler := handlers.NewWalletHandler(db)

	router.Route("/transaction/", func(trans chi.Router) {
		trans.Post("/initiate", transHandler.Initiate)
	})

	router.Route("/profile", func(profile chi.Router) {
		profile.Post("/register", profileHandler.Register)
	})

	router.Route("/wallet", func(wallet chi.Router) {
		wallet.Post("/create", walletHandler.Create)
	})

	return router
}
