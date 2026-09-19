package servers

import (
	"net"

	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	grpchandlers "github.com/katuva/wallet/internal/handlers/grpc"
	"github.com/katuva/wallet/internal/routers"
	pbprofile "github.com/katuva/wallet/proto/profile"
	pbtransaction "github.com/katuva/wallet/proto/transaction"
	pbwallet "github.com/katuva/wallet/proto/wallet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// startGrpc serves in the background and reports a fatal serve error on the
// returned channel.
func startGrpc(cfg *config.Config, svc routers.Services) (*grpc.Server, <-chan error) {
	errCh := make(chan error, 1)

	lis, err := net.Listen("tcp", ":"+cfg.GrpcPort)
	if err != nil {
		errCh <- err
		return grpc.NewServer(), errCh
	}

	server := grpc.NewServer()
	pbprofile.RegisterProfileServiceServer(server, grpchandlers.NewProfileGrpcHandler(svc.Profiles))
	pbwallet.RegisterWalletServiceServer(server, grpchandlers.NewWalletGrpcHandler(svc.Wallets))
	pbtransaction.RegisterTransactionServiceServer(server, grpchandlers.NewTransactionGrpcHandler(svc.Transactions))
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)

	go func() {
		logger.InfoLog.Println("gRPC server listening on :" + cfg.GrpcPort)
		errCh <- server.Serve(lis)
	}()
	return server, errCh
}
