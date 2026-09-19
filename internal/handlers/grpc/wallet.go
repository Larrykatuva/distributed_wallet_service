package grpc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"github.com/katuva/wallet/internal/validation"
	pb "github.com/katuva/wallet/proto/wallet"
)

type WalletGrpcHandler struct {
	pb.UnimplementedWalletServiceServer
	wallets *services.WalletService
}

func NewWalletGrpcHandler(wallets *services.WalletService) *WalletGrpcHandler {
	return &WalletGrpcHandler{wallets: wallets}
}

func (h *WalletGrpcHandler) Create(_ context.Context, req *pb.CreateWalletRequest) (*pb.WalletResponse, error) {
	fe := validation.FieldErrors{}
	profileID, err := uuid.Parse(req.ProfileId)
	if err != nil {
		fe["profile_id"] = "is not a valid UUID"
	}
	merchantID, err := uuid.Parse(req.MerchantId)
	if err != nil {
		fe["merchant_id"] = "is not a valid UUID"
	}
	if len(fe) > 0 {
		return nil, toStatus(fe)
	}

	payload := types.WalletReqDto{ProfileID: profileID, MerchantID: merchantID, Currency: req.Currency}
	if err := validation.Struct(&payload); err != nil {
		return nil, toStatus(err)
	}

	wallet, err := h.wallets.Create(payload)
	if err != nil {
		return nil, toStatus(err)
	}
	return walletToProto(wallet), nil
}

func (h *WalletGrpcHandler) Get(_ context.Context, req *pb.GetWalletRequest) (*pb.WalletResponse, error) {
	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, toStatus(validation.FieldErrors{"id": "is not a valid UUID"})
	}

	wallet, err := h.wallets.Get(id)
	if err != nil {
		return nil, toStatus(err)
	}
	return walletToProto(wallet), nil
}

func walletToProto(w types.WalletResDto) *pb.WalletResponse {
	return &pb.WalletResponse{
		Id:                w.ID.String(),
		CreatedAt:         w.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         w.UpdatedAt.Format(time.RFC3339),
		Number:            w.Number,
		Status:            string(w.Status),
		Currency:          w.Currency,
		AvailableBalance:  w.AvailableBalance.String(),
		ProcessingBalance: w.ProcessingBalance.String(),
		ActualBalance:     w.ActualBalance.String(),
		Version:           w.Version,
		Profile:           &pb.ProfileDto{Id: w.Profile.ID.String(), FullName: w.Profile.FullName},
		Merchant:          &pb.ProfileDto{Id: w.Merchant.ID.String(), FullName: w.Merchant.FullName},
	}
}
