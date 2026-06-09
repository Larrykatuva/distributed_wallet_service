package grpc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	pb "github.com/katuva/wallet/proto/wallet"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type WalletGrpcHandler struct {
	pb.UnimplementedWalletServiceServer
	walletService *services.WalletServiceImpl
}

func NewWalletGrpcHandler(db *gorm.DB) *WalletGrpcHandler {
	return &WalletGrpcHandler{
		walletService: services.NewWalletService(db),
	}
}

func (h *WalletGrpcHandler) Create(
	ctx context.Context,
	req *pb.CreateWalletRequest,
) (*pb.CreateWalletResponse, error) {

	// Validate
	if req.ProfileId == "" {
		return nil, status.Error(codes.InvalidArgument, "profile_id is required")
	}
	if req.MerchantId == "" {
		return nil, status.Error(codes.InvalidArgument, "merchant_id is required")
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, "currency is required")
	}

	// Parse UUIDs
	profileID, err := uuid.Parse(req.ProfileId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "profile_id is not a valid UUID")
	}
	merchantID, err := uuid.Parse(req.MerchantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "merchant_id is not a valid UUID")
	}

	// Build DTO
	payload := types.WalletReqDto{
		ProfileID:  profileID,
		MerchantID: merchantID,
		Currency:   req.Currency,
	}

	// Call service
	wallet, err := h.walletService.Create(payload)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &pb.CreateWalletResponse{
		Id:                wallet.ID.String(),
		CreatedAt:         wallet.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         wallet.UpdatedAt.Format(time.RFC3339),
		Number:            wallet.Number,
		Status:            string(wallet.Status),
		Currency:          wallet.Currency,
		AvailableBalance:  wallet.AvailableBalance,
		ProcessingBalance: wallet.ProcessingBalance,
		ActualBalance:     wallet.ActualBalance,
		Version:           wallet.Version,
		Profile: &pb.ProfileDto{
			Id:       wallet.Profile.ID.String(),
			FullName: wallet.Profile.FullName,
		},
		Merchant: &pb.ProfileDto{
			Id:       wallet.Merchant.ID.String(),
			FullName: wallet.Merchant.FullName,
		},
	}, nil
}
