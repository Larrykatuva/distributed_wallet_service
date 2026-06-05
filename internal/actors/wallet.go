package actors

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
	"gorm.io/gorm"
)

// WalletActor is a proto.actor actor responsible for managing a single wallet's
// balance operations. One actor instance exists per wallet ID — the actor model
// guarantees that all operations on the same wallet are serialised through its
// mailbox, eliminating the need for database-level locks.
type WalletActor struct {
	db       *gorm.DB
	walletID uuid.UUID
}

// NewWalletActor creates a new WalletActor for the given wallet ID.
// The db connection is injected so the actor can persist balance changes
// and ledger entries to PostgreSQL.
func NewWalletActor(db *gorm.DB, walletID uuid.UUID) actor.Actor {
	return &WalletActor{
		db:       db,
		walletID: walletID,
	}
}

// Receive is the actor's message handler. Proto.actor guarantees this method
// is never called concurrently for the same actor — messages are processed
// one at a time from the mailbox, making all balance mutations thread-safe
// without explicit locking.
func (w *WalletActor) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {

	// CreditWalletMsg increases the wallet balance.
	// Sent by TransactionActor for credit transfers and debit reversals.
	case CreditWalletMsg:
		resultMsg := WalletResultMsg{
			WalletID:       w.walletID,
			TransactionID:  msg.TransactionID,
			IdempotencyKey: msg.IdempotencyKey,
			Amount:         msg.Amount,
		}

		// Validate wallet status — credits do not check balance since
		// we are adding funds, not subtracting them
		wallet, err := w.getValidatedWallet(nil)
		if err != nil {
			resultMsg.Error = err
		} else {
			resultMsg.InitialBalance = wallet.AvailableBalance
			resultMsg.UpdatedBalance = wallet.AvailableBalance

			if err = w.handleCredit(wallet, msg); err != nil {
				resultMsg.Error = err
			} else {
				// Reflect the new balance in the result after successful credit
				resultMsg.InitialBalance = wallet.ActualBalance
				resultMsg.UpdatedBalance = wallet.ActualBalance + msg.Amount
			}
		}

		// Reply to the TransactionActor that sent this message.
		// ctx.Sender() is set because TransactionActor uses ctx.Request.
		ctx.Send(ctx.Sender(), resultMsg)

	// DebitWalletMsg decreases the wallet balance.
	// Sent by TransactionActor for debit transfers and credit reversals.
	case DebitWalletMsg:
		resultMsg := WalletResultMsg{
			WalletID:       w.walletID,
			TransactionID:  msg.TransactionID,
			IdempotencyKey: msg.IdempotencyKey,
			Amount:         msg.Amount,
		}

		// Validate wallet status and check sufficient funds.
		// Passing &msg.Amount triggers the balance check in getValidatedWallet.
		wallet, err := w.getValidatedWallet(&msg.Amount)
		if err != nil {
			resultMsg.Error = err
		} else {
			resultMsg.InitialBalance = wallet.AvailableBalance
			resultMsg.UpdatedBalance = wallet.AvailableBalance

			if err = w.handleDebit(wallet, msg); err != nil {
				resultMsg.Error = err
			} else {
				// Reflect the new balance in the result after successful debit
				resultMsg.InitialBalance = wallet.ActualBalance
				resultMsg.UpdatedBalance = wallet.ActualBalance - msg.Amount
			}
		}

		ctx.Send(ctx.Sender(), resultMsg)
	}
}

// getValidatedWallet fetches the wallet from the database and validates it.
// If amount is non-nil, it also checks that the wallet has sufficient funds.
// Returns an error if the wallet is not found, inactive, or underfunded.
func (w *WalletActor) getValidatedWallet(amount *float64) (*models.Wallet, error) {
	var wallet models.Wallet

	result := w.db.
		Model(models.Wallet{}).
		First(&wallet, "id = ?", w.walletID)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, errors.New(string(WalletNotFoundMsg))
		}
		return nil, result.Error
	}

	// Only active wallets can process transactions.
	// Suspended, frozen, and closed wallets are rejected here.
	if wallet.Status != "active" {
		return nil, errors.New(string(InactiveWalletMsg))
	}

	// For debits only — ensure the wallet has enough available balance
	// before proceeding. Credits skip this check.
	if amount != nil {
		if wallet.AvailableBalance < *amount {
			return nil, errors.New(string(InsufficientFundsMsg))
		}
	}

	return &wallet, nil
}

// handleCredit applies a credit to the wallet inside a database transaction.
// Both the wallet balance update and the ledger entry are written atomically —
// if either fails, both are rolled back.
func (w *WalletActor) handleCredit(wallet *models.Wallet, msg CreditWalletMsg) error {
	return w.db.Transaction(func(tx *gorm.DB) error {
		actualBalance := wallet.ActualBalance + msg.Amount

		// Update both available and actual balances, bump the version for
		// optimistic concurrency tracking, and recompute the checksum for
		// tamper detection.
		err := tx.
			Model(models.Wallet{}).
			Where("id = ?", w.walletID).
			Updates(map[string]any{
				"available_balance": actualBalance,
				"actual_balance":    actualBalance,
				"version":           gorm.Expr("version + 1"),
				"checksum":          computeChecksum(w.walletID, actualBalance, wallet.Version+1),
				"updated_at":        time.Now(),
			}).Error
		if err != nil {
			return err
		}

		// Write an immutable ledger record for this credit.
		// The ledger is the source of truth for all balance movements.
		err = tx.Create(&models.Ledger{
			WalletID:       w.walletID,
			TransactionID:  msg.TransactionID,
			Type:           models.LedgerTypeCredit,
			Purpose:        msg.Purpose,
			Amount:         msg.Amount,
			Currency:       wallet.Currency,
			InitialBalance: wallet.ActualBalance,
			UpdatedBalance: actualBalance,
		}).Error
		if err != nil {
			return err
		}

		return nil
	})
}

// handleDebit applies a debit to the wallet inside a database transaction.
// Both the wallet balance update and the ledger entry are written atomically —
// if either fails, both are rolled back.
func (w *WalletActor) handleDebit(wallet *models.Wallet, msg DebitWalletMsg) error {
	return w.db.Transaction(func(tx *gorm.DB) error {
		actualBalance := wallet.ActualBalance - msg.Amount

		// Update both available and actual balances, bump the version for
		// optimistic concurrency tracking, and recompute the checksum for
		// tamper detection.
		err := tx.
			Model(models.Wallet{}).
			Where("id = ?", w.walletID).
			Updates(map[string]any{
				"actual_balance":    actualBalance,
				"available_balance": actualBalance,
				"version":           gorm.Expr("version + 1"),
				"checksum":          computeChecksum(w.walletID, actualBalance, wallet.Version+1),
				"updated_at":        time.Now(),
			}).Error
		if err != nil {
			return err
		}

		// Write an immutable ledger record for this debit.
		// The ledger is the source of truth for all balance movements.
		err = tx.Create(&models.Ledger{
			WalletID:       w.walletID,
			TransactionID:  msg.TransactionID,
			Type:           models.LedgerTypeDebit,
			Purpose:        msg.Purpose,
			Amount:         msg.Amount,
			Currency:       wallet.Currency,
			InitialBalance: wallet.ActualBalance,
			UpdatedBalance: actualBalance,
		}).Error
		if err != nil {
			return err
		}

		return nil
	})
}

// computeChecksum generates a SHA-256 hash of the wallet's critical fields.
// Stored in wallet.Checksum after every balance mutation — any out-of-band
// database modification will cause a checksum mismatch detectable on next read.
func computeChecksum(walletID uuid.UUID, available float64, version int64) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s:%d:%d:%d", walletID, available, version)))
	return fmt.Sprintf("%x", h.Sum(nil))
}
