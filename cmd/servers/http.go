package servers

import (
	"net/http"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/routers"
	"gorm.io/gorm"
)

func StartHttpServer(cfg *config.Config, db *gorm.DB, cluster *cluster.Cluster) {
	mainRouter := chi.NewRouter()

	mainRouter.Use(middleware.AllowContentType(
		"application/json",
	))

	mainRouter.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	mainRouter.Mount("/v1/", routers.Router(db, cluster))

	// Register Dead latter handler
	actors.NewDeadLatterActor(db, cluster).RegisterHandler()

	logger.InfoLog.Println("Server is up")
	if err := http.ListenAndServe(":"+cfg.Port, mainRouter); err != nil {
		logger.ErrorLog.Println(err)
	}
	logger.InfoLog.Println("Server listening to port: " + cfg.Port)
}
