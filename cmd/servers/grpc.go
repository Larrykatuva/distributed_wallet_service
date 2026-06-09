package servers

import (
	"net"

	"github.com/asynkron/protoactor-go/cluster"
	grpc2 "github.com/katuva/wallet/internal/handlers/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"

	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
	pbprofile "github.com/katuva/wallet/proto/profile"
	pbtransaction "github.com/katuva/wallet/proto/transaction"
	pbwallet "github.com/katuva/wallet/proto/wallet"
)

func StartGrpcServer(cfg *config.Config, db *gorm.DB, cluster *cluster.Cluster) {
	lis, err := net.Listen("tcp", ":"+cfg.GrpcPort)
	if err != nil {
		logger.ErrorLog.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	pbprofile.RegisterProfileServiceServer(grpcServer, grpc2.NewProfileGrpcHandler(db))
	pbwallet.RegisterWalletServiceServer(grpcServer, grpc2.NewWalletGrpcHandler(db))
	pbtransaction.RegisterTransactionServiceServer(grpcServer, grpc2.NewTransactionGrpcHandler(db, cluster))

	reflection.Register(grpcServer)

	logger.InfoLog.Println("gRPC server listening on port: " + cfg.GrpcPort)
	if err = grpcServer.Serve(lis); err != nil {
		logger.ErrorLog.Fatalf("failed to serve: %v", err)
	}
}
