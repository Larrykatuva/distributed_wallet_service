package services

import (
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

// TransactionPayload returns a builder that renders a transaction for the API
// and callbacks. When the orchestrator supplies the balances it observed, no
// ledger read happens; otherwise (GET, recovery) they are derived from the
// ledger.
func TransactionPayload(db *gorm.DB) func(models.Transaction, []models.WalletMovement) types.TransactionResDto {
	return func(tx models.Transaction, movements []models.WalletMovement) types.TransactionResDto {
		dto := types.TransactionFromModel(tx)
		if movements != nil {
			dto.Balances = fromMovements(tx.Currency, movements)
		} else {
			dto.Balances = walletBalances(db, tx)
		}
		return dto
	}
}

func fromMovements(currencyCode string, movements []models.WalletMovement) []types.WalletBalanceDto {
	exp := 2
	if c, ok := currency.Lookup(currencyCode); ok {
		exp = c.Exponent
	}
	out := make([]types.WalletBalanceDto, 0, len(movements))
	for _, m := range movements {
		out = append(out, types.WalletBalanceDto{
			WalletID: m.WalletID, Currency: m.Currency,
			InitialBalance: types.MoneyFromMinor(m.InitialBalance, exp),
			UpdatedBalance: types.MoneyFromMinor(m.UpdatedBalance, exp),
			Entries:        m.Entries,
		})
	}
	return out
}

// walletBalances folds the transaction's ledger rows (including reversals)
// into one entry per wallet: the balance before the first movement and after
// the last. For a reversed transaction both ends match.
func walletBalances(db *gorm.DB, tx models.Transaction) []types.WalletBalanceDto {
	var rows []models.Ledger
	err := db.
		Select("id", "wallet_id", "currency", "initial_balance", "updated_balance").
		Where("transaction_id = ? AND created_at >= ?", tx.ID, tx.CreatedAt.Add(-time.Minute)).
		Order("id ASC").
		Find(&rows).Error
	if err != nil {
		logger.ErrorLog.Printf("txn %s: load ledger for balances: %v", tx.ID, err)
		return []types.WalletBalanceDto{}
	}

	exp := 2
	if c, ok := currency.Lookup(tx.Currency); ok {
		exp = c.Exponent
	}

	order := make([]uuid.UUID, 0, 4)
	byWallet := map[uuid.UUID]*types.WalletBalanceDto{}
	for _, r := range rows {
		entry, seen := byWallet[r.WalletID]
		if !seen {
			entry = &types.WalletBalanceDto{
				WalletID:       r.WalletID,
				Currency:       r.Currency,
				InitialBalance: types.MoneyFromMinor(r.InitialBalance, exp),
			}
			byWallet[r.WalletID] = entry
			order = append(order, r.WalletID)
		}
		entry.UpdatedBalance = types.MoneyFromMinor(r.UpdatedBalance, exp)
		entry.Entries++
	}

	out := make([]types.WalletBalanceDto, 0, len(order))
	for _, id := range order {
		out = append(out, *byWallet[id])
	}
	return out
}
