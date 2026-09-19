package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"github.com/katuva/wallet/internal/validation"
	pb "github.com/katuva/wallet/proto/transaction"
)

type TransactionGrpcHandler struct {
	pb.UnimplementedTransactionServiceServer
	transactions *services.TransactionService
}

func NewTransactionGrpcHandler(transactions *services.TransactionService) *TransactionGrpcHandler {
	return &TransactionGrpcHandler{transactions: transactions}
}

func (h *TransactionGrpcHandler) Initiate(_ context.Context, req *pb.InitiateTransactionRequest) (*pb.TransactionResponse, error) {
	fe := validation.FieldErrors{}
	merchantID, err := uuid.Parse(req.MerchantId)
	if err != nil {
		fe["merchant_id"] = "is not a valid UUID"
	}
	total, err := types.ParseMoney(req.TotalAmount)
	if err != nil {
		fe["total_amount"] = err.Error()
	}
	fee := types.MoneyFromMinor(0, 0)
	if req.Fee != "" {
		if fee, err = types.ParseMoney(req.Fee); err != nil {
			fe["fee"] = err.Error()
		}
	}
	transfers := make([]types.TransactionEntryDto, 0, len(req.Transfers))
	for i, t := range req.Transfers {
		walletID, err := uuid.Parse(t.WalletId)
		if err != nil {
			fe[fmt.Sprintf("transfers[%d].wallet_id", i)] = "is not a valid UUID"
		}
		amount, err := types.ParseMoney(t.Amount)
		if err != nil {
			fe[fmt.Sprintf("transfers[%d].amount", i)] = err.Error()
		}
		transfers = append(transfers, types.TransactionEntryDto{
			WalletID:  walletID,
			Action:    models.LedgerType(t.Action),
			Amount:    amount,
			Purpose:   t.Purpose,
			IsFee:     t.IsFee,
			IsInitial: t.IsInitial,
		})
	}
	if len(fe) > 0 {
		return nil, toStatus(fe)
	}

	payload := types.TransactionReqDto{
		Type:        models.TransactionType(req.Type),
		MerchantID:  merchantID,
		OrderId:     req.OrderId,
		ProviderRef: req.ProviderRef,
		CallbackUrl: req.CallbackUrl,
		TotalAmount: total,
		Fee:         fee,
		Currency:    req.Currency,
		Purpose:     req.Purpose,
		Description: req.Description,
		Transfers:   transfers,
	}
	if err := validation.Struct(&payload); err != nil {
		return nil, toStatus(err)
	}

	result, err := h.transactions.Initiate(payload)
	if err != nil {
		return nil, toStatus(err)
	}
	return transactionToProto(result), nil
}

func (h *TransactionGrpcHandler) Get(_ context.Context, req *pb.GetTransactionRequest) (*pb.TransactionResponse, error) {
	var (
		result types.TransactionResDto
		err    error
	)
	switch {
	case req.Rrn != nil && *req.Rrn != "":
		result, err = h.transactions.GetByRRN(*req.Rrn)
	case req.OrderId != nil && *req.OrderId != "":
		result, err = h.transactions.GetByOrderID(*req.OrderId)
	default:
		return nil, toStatus(validation.FieldErrors{"rrn": "or order_id is required"})
	}
	if err != nil {
		return nil, toStatus(err)
	}
	return transactionToProto(result), nil
}

func transactionToProto(t types.TransactionResDto) *pb.TransactionResponse {
	out := &pb.TransactionResponse{
		Id:          t.ID.String(),
		OrderId:     t.OrderID,
		ProviderRef: t.ProviderRef,
		Rrn:         t.Rrn,
		CallbackUrl: t.CallbackURL,
		Type:        string(t.Type),
		Status:      string(t.Status),
		Narration:   t.Narration,
		Amount:      t.Amount.String(),
		Fee:         t.Fee.String(),
		Currency:    t.Currency,
		IsCompleted: t.IsCompleted,
		CreatedAt:   t.CreatedAt.Format(time.RFC3339),
	}
	if t.MerchantID != nil {
		m := t.MerchantID.String()
		out.MerchantId = &m
	}
	for _, b := range t.Balances {
		out.Balances = append(out.Balances, &pb.WalletBalanceDto{
			WalletId: b.WalletID.String(), Currency: b.Currency,
			InitialBalance: b.InitialBalance.String(), UpdatedBalance: b.UpdatedBalance.String(),
			Entries: int32(b.Entries),
		})
	}
	for _, l := range t.Transfers {
		out.Transfers = append(out.Transfers, &pb.TransactionEntryDto{
			WalletId: l.WalletID.String(), Action: string(l.Action), Amount: l.Amount.String(),
			Purpose: l.Purpose, IsFee: l.IsFee, IsInitial: l.IsInitial,
		})
	}
	if t.DateCompleted != nil {
		s := t.DateCompleted.Format(time.RFC3339)
		out.DateCompleted = &s
	}
	return out
}
