// Package servers boots the cluster, the gRPC server and the HTTP server and
// shuts them down in order on SIGINT/SIGTERM.
package servers

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/cluster"
	"github.com/katuva/wallet/internal/db"
	"github.com/katuva/wallet/internal/recovery"
	"github.com/katuva/wallet/internal/routers"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/webhook"
	"gorm.io/gorm"
)

// Run blocks until a termination signal arrives, then drains everything.
func Run(cfg *config.Config, gdb *gorm.DB) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	notifier := webhook.NewNotifier(services.TransactionPayload(gdb))

	c, err := cluster.New(cfg, cluster.Kinds(gdb, notifier, cfg.TransactionTimeout))
	if err != nil {
		return err
	}

	recoverer := recovery.NewManager(gdb, c, notifier, recovery.Options{
		MaxAttempts:   cfg.RecoveryMaxAttempts,
		StaleAfter:    cfg.RecoveryStaleAfter,
		SweepInterval: cfg.RecoverySweepInterval,
	})
	actors.RegisterDeadLetterHandler(c.ActorSystem, recoverer)
	go recoverer.RunSweeper(ctx)

	svc := routers.Services{
		Profiles:     services.NewProfileService(gdb),
		Wallets:      services.NewWalletService(gdb),
		Transactions: services.NewTransactionService(gdb, c, notifier),
	}

	go db.RunPartitionMaintenance(ctx, gdb)

	grpcServer, grpcErr := startGrpc(cfg, svc)
	httpServer, httpErr := startHTTP(cfg, gdb, svc)

	select {
	case <-ctx.Done():
		logger.InfoLog.Println("shutdown signal received")
	case err = <-grpcErr:
		logger.ErrorLog.Printf("grpc server stopped: %v", err)
	case err = <-httpErr:
		logger.ErrorLog.Printf("http server stopped: %v", err)
	}

	// Stop accepting work first, then leave the cluster so in-flight actor
	// messages have a chance to complete and peers see a clean departure.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if herr := httpServer.Shutdown(shutdownCtx); herr != nil && !errors.Is(herr, http.ErrServerClosed) {
		logger.ErrorLog.Printf("http shutdown: %v", herr)
	}
	grpcServer.GracefulStop()

	// Give already-dispatched actor work a short grace period.
	time.Sleep(500 * time.Millisecond)
	c.Shutdown(true)
	logger.InfoLog.Println("shutdown complete")
	return err
}
