package grpc

import (
	"context"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	pb "github.com/katuva/wallet/proto/transaction"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type TransactionGrpcHandler struct {
	pb.UnimplementedTransactionServiceServer
	transactionService *services.TransactionServiceImpl
}

func NewTransactionGrpcHandler(db *gorm.DB, cluster *cluster.Cluster) *TransactionGrpcHandler {
	return &TransactionGrpcHandler{
		transactionService: services.NewTransactionService(db, cluster),
	}
}

func (h *TransactionGrpcHandler) Initiate(
	ctx context.Context,
	req *pb.InitiateTransactionRequest,
) (*pb.InitiateTransactionResponse, error) {

	// Validate
	if req.Type == "" {
		return nil, status.Error(codes.InvalidArgument, "type is required")
	}
	txType := models.TransactionType(req.Type)
	validTypes := map[models.TransactionType]bool{
		models.TransactionTypeTransfer:   true,
		models.TransactionTypeDeposit:    true,
		models.TransactionTypeWithdrawal: true,
		models.TransactionTypePayment:    true,
		models.TransactionTypeReversal:   true,
		models.TransactionTypeAdjustment: true,
	}
	if !validTypes[txType] {
		return nil, status.Error(codes.InvalidArgument, "type must be one of: transfer, deposit, withdrawal, payment, reversal, adjustment")
	}
	if req.OrderId == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if req.ProviderRef == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_ref is required")
	}
	if req.CallbackUrl == "" {
		return nil, status.Error(codes.InvalidArgument, "callback_url is required")
	}
	if req.TotalAmount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_amount must be greater than 0")
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, "currency is required")
	}
	if len(req.Transfers) == 0 {
		return nil, status.Error(codes.InvalidArgument, "transfers must have at least 1 entry")
	}

	// Map transfers
	transfers := make([]types.TransactionEntryDto, 0, len(req.Transfers))
	for i, t := range req.Transfers {
		if t.WalletId == "" {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: wallet_id is required", i)
		}
		walletID, err := uuid.Parse(t.WalletId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: wallet_id is not a valid UUID", i)
		}
		if t.Action == "" {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: action is required", i)
		}
		action := models.LedgerType(t.Action)
		if action != models.LedgerTypeDebit && action != models.LedgerTypeCredit {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: action must be one of: debit, credit", i)
		}
		if t.Amount <= 0 {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: amount must be greater than 0", i)
		}
		if t.Purpose == "" {
			return nil, status.Errorf(codes.InvalidArgument, "transfers[%d]: purpose is required", i)
		}

		transfers = append(transfers, types.TransactionEntryDto{
			WalletID:  walletID,
			Action:    action,
			Amount:    t.Amount,
			Purpose:   t.Purpose,
			IsFee:     t.IsFee,
			IsInitial: t.IsInitial,
		})
	}

	// Build DTO
	payload := types.TransactionReqDto{
		Type:        txType,
		OrderId:     req.OrderId,
		ProviderRef: req.ProviderRef,
		CallbackUrl: req.CallbackUrl,
		TotalAmount: req.TotalAmount,
		Fee:         req.Fee,
		Currency:    req.Currency,
		Purpose:     req.Purpose,
		Description: req.Description,
		Transfers:   transfers,
	}

	// Call service
	result, err := h.transactionService.Initiate(payload)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &pb.InitiateTransactionResponse{
		OrderId:     result.OrderID,
		ProviderRef: result.ProviderRef,
		Rrn:         result.Rrn,
		Type:        string(result.Type),
		Status:      string(result.Status),
		Narration:   result.Narration,
	}, nil
}
