package services

import (
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/utils"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type TransactionInterface interface {
	process(payload types.TransactionReqDto, wallets []models.Wallet) (models.Transaction, error)
	Initiate(payload types.TransactionReqDto) (types.TransactionResDto, error)
}

type TransactionServiceImpl struct {
	db             *gorm.DB
	walletService  *WalletServiceImpl
	profileService *ProfileServiceImpl
	cluster        *cluster.Cluster
}

func NewTransactionService(db *gorm.DB, cluster *cluster.Cluster) *TransactionServiceImpl {
	return &TransactionServiceImpl{
		db:             db,
		walletService:  NewWalletService(db),
		profileService: NewProfileService(db),
		cluster:        cluster,
	}
}

func (ts *TransactionServiceImpl) process(payload types.TransactionReqDto, wallets []models.Wallet) (models.Transaction, error) {
	var walletFrom, walletTo *models.Wallet

	for _, transfer := range payload.Transfers {
		for _, wallet := range wallets {
			if wallet.ID == transfer.WalletID && transfer.IsInitial {
				if transfer.Action == models.LedgerTypeDebit {
					walletFrom = &wallet
				} else {
					walletTo = &wallet
				}
			}
		}
	}

	transaction := models.Transaction{
		Amount:      payload.TotalAmount,
		Currency:    payload.Currency,
		Fee:         payload.Fee,
		Type:        payload.Type,
		Purpose:     payload.Purpose,
		RRN:         utils.GenerateRrn(),
		OrderID:     &payload.OrderId,
		ProviderRef: &payload.ProviderRef,
		Description: payload.Description,
		Narration:   "Transaction accepted for processing",
	}

	if walletFrom != nil {
		transaction.MerchantFrom = &walletFrom.Merchant.ID
		transaction.ProfileFrom = &walletFrom.ProfileID
		transaction.SenderName = &walletFrom.Profile.FullName
		transaction.SenderMerchant = &walletFrom.Merchant.FullName
		transaction.AccountFrom = &walletFrom.Number
		transaction.WalletFrom = &walletFrom.ID
	}

	if walletTo != nil {
		transaction.MerchantTo = &walletTo.Merchant.ID
		transaction.ProfileTo = &walletTo.ProfileID
		transaction.ReceiverName = &walletTo.Profile.FullName
		transaction.ReceiverMerchant = &walletTo.Merchant.FullName
		transaction.AccountTo = &walletTo.Number
		transaction.WalletTo = &walletTo.ID
	}

	if err := ts.db.Create(&transaction).Error; err != nil {
		return transaction, err
	}

	return transaction, nil
}

func (ts *TransactionServiceImpl) Initiate(payload types.TransactionReqDto) (types.TransactionResDto, error) {
	type walletsResult struct {
		wallets []models.Wallet
		err     error
	}

	walletsCh := make(chan walletsResult, 1)

	go func() {
		walletIds := make([]uuid.UUID, 0, len(payload.Transfers))
		for _, entry := range payload.Transfers {
			walletIds = append(walletIds, entry.WalletID)
		}
		wallets, err := ts.walletService.ValidateWallets(walletIds)
		walletsCh <- walletsResult{wallets, err}
	}()

	walletsRes := <-walletsCh

	if walletsRes.err != nil {
		return types.TransactionResDto{}, walletsRes.err
	}

	transaction, err := ts.process(payload, walletsRes.wallets)
	if err != nil {
		return types.TransactionResDto{}, err
	}

	transfers := make([]actors.TransferEntry, len(payload.Transfers))
	for i, t := range payload.Transfers {
		transfers[i] = actors.TransferEntry{
			WalletID:       t.WalletID,
			Action:         t.Action,
			Amount:         t.Amount,
			Purpose:        t.Purpose,
			IdempotencyKey: uuid.New(),
		}
	}

	txnPID := actors.SpawnTransactionActor(ts.db, ts.cluster, transaction.ID)

	ts.cluster.ActorSystem.Root.Send(txnPID, actors.StartTransactionMsg{
		TransactionID: transaction.ID,
		Date:          transaction.CreatedAt.AddDate(0, 0, -1),
		Transfers:     transfers,
	})

	return types.TransactionResDto{
		OrderID:     transaction.OrderID,
		ProviderRef: transaction.ProviderRef,
		Rrn:         transaction.RRN,
		Type:        transaction.Type,
		Status:      transaction.Status,
		Narration:   transaction.Narration,
	}, nil
}
