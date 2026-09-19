package actors

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/models"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
)

// maxVersionRetries bounds how many times a balance update is retried after
// an optimistic-concurrency conflict. Conflicts should only come from
// out-of-band writes because the actor serialises its own wallet.
const maxVersionRetries = 3

// WalletActor owns a single wallet's balance. One instance exists per wallet
// ID (cluster grain identity), so all mutations on that wallet are serialised
// through the mailbox and no database row locks are needed.
type WalletActor struct {
	db       *gorm.DB
	walletID uuid.UUID

	// wallet is this actor's copy of the row. Being the only writer, the
	// actor can trust it between messages and skip a SELECT per operation;
	// the version-guarded UPDATE still catches any out-of-band change, on
	// which the copy is dropped and reloaded.
	wallet *models.Wallet
}

// NewWalletActor is the cluster kind producer. The wallet ID arrives via
// ClusterInit (grain identity) and is cross-checked against every message.
func NewWalletActor(db *gorm.DB) actor.Actor {
	return &WalletActor{db: db}
}

// NewWalletActorFor builds an actor bound to a known wallet, for local spawns.
func NewWalletActorFor(db *gorm.DB, walletID uuid.UUID) actor.Actor {
	return &WalletActor{db: db, walletID: walletID}
}

func (w *WalletActor) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {
	case *cluster.ClusterInit:
		if id, err := uuid.Parse(msg.Identity.Identity); err == nil {
			w.walletID = id
		} else {
			logger.ErrorLog.Printf("wallet actor: bad grain identity %q: %v", msg.Identity.Identity, err)
		}

	case *pb.CreditWalletMsg:
		w.handle(ctx, models.LedgerTypeCredit, msg.WalletId, msg.TransactionId, msg.IdempotencyKey, msg.Amount, msg.Purpose)

	case *pb.DebitWalletMsg:
		w.handle(ctx, models.LedgerTypeDebit, msg.WalletId, msg.TransactionId, msg.IdempotencyKey, msg.Amount, msg.Purpose)
	}
}

type applyResult struct {
	initial, updated int64
}

func (w *WalletActor) handle(ctx actor.Context, action models.LedgerType, walletIDStr, txIDStr, keyStr string, amount int64, purpose string) {
	reply := &pb.WalletResultMsg{
		WalletId:       walletIDStr,
		TransactionId:  txIDStr,
		IdempotencyKey: keyStr,
		Amount:         amount,
	}

	res, err := w.apply(action, walletIDStr, txIDStr, keyStr, amount, purpose)
	if err != nil {
		we := wrapInternal(err)
		reply.ErrorCode = string(we.Code)
		reply.ErrorMessage = we.Message
		if we.Code == CodeInternal || we.Code == CodeChecksumMismatch {
			logger.ErrorLog.Printf("wallet %s %s failed: %v", walletIDStr, action, err)
		}
	} else {
		reply.InitialBalance = res.initial
		reply.UpdatedBalance = res.updated
	}

	if ctx.Sender() != nil {
		ctx.Respond(reply)
	}
}

func (w *WalletActor) apply(action models.LedgerType, walletIDStr, txIDStr, keyStr string, amount int64, purpose string) (applyResult, error) {
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		return applyResult{}, newWalletError(CodeInvalidMessage, "wallet_id is not a UUID")
	}
	txID, err := uuid.Parse(txIDStr)
	if err != nil {
		return applyResult{}, newWalletError(CodeInvalidMessage, "transaction_id is not a UUID")
	}
	key, err := uuid.Parse(keyStr)
	if err != nil {
		return applyResult{}, newWalletError(CodeInvalidMessage, "idempotency_key is not a UUID")
	}
	if amount <= 0 {
		return applyResult{}, newWalletError(CodeInvalidMessage, "amount must be positive")
	}
	if w.walletID != uuid.Nil && w.walletID != walletID {
		return applyResult{}, newWalletError(CodeWalletMismatch,
			fmt.Sprintf("message for %s delivered to actor for %s", walletID, w.walletID))
	}

	// Idempotency: a redelivered message must not move the balance twice.
	// Safe without locking because this actor is the only writer for the wallet.
	if prior, found, err := w.findLedger(walletID, key); err != nil {
		return applyResult{}, wrapInternal(err)
	} else if found {
		return applyResult{initial: prior.InitialBalance, updated: prior.UpdatedBalance}, nil
	}

	var lastErr error
	for attempt := 0; attempt < maxVersionRetries; attempt++ {
		wallet, err := w.current(walletID)
		if err != nil {
			return applyResult{}, err
		}

		if action == models.LedgerTypeDebit && wallet.AvailableBalance < amount {
			return applyResult{}, newWalletError(CodeInsufficientFunds,
				fmt.Sprintf("available %d < requested %d", wallet.AvailableBalance, amount))
		}

		res, err := w.persist(wallet, action, txID, key, amount, purpose)
		if err == nil {
			return res, nil
		}
		lastErr = err
		w.wallet = nil // whatever we held is stale
		var we *WalletError
		if !errors.As(err, &we) || we.Code != CodeVersionConflict {
			return applyResult{}, wrapInternal(err)
		}
	}
	return applyResult{}, wrapInternal(lastErr)
}

// current returns the in-memory wallet, loading it on first use.
func (w *WalletActor) current(walletID uuid.UUID) (*models.Wallet, error) {
	if w.wallet != nil && w.wallet.ID == walletID {
		if w.wallet.Status != models.WalletStatusActive {
			return nil, newWalletError(CodeInactiveWallet, string(w.wallet.Status))
		}
		return w.wallet, nil
	}
	wallet, err := w.load(walletID)
	if err != nil {
		return nil, err
	}
	w.wallet = wallet
	return wallet, nil
}

// load fetches the wallet and enforces status and checksum integrity.
func (w *WalletActor) load(walletID uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	err := w.db.First(&wallet, "id = ?", walletID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, newWalletError(CodeWalletNotFound, walletID.String())
		}
		return nil, wrapInternal(err)
	}

	if wallet.Status != models.WalletStatusActive {
		return nil, newWalletError(CodeInactiveWallet, string(wallet.Status))
	}

	// An empty checksum means the row predates checksumming; it is stamped on
	// the next successful mutation. Any other mismatch is tampering or a bug.
	if wallet.Checksum != "" && wallet.Checksum != ComputeChecksum(&wallet) {
		return nil, newWalletError(CodeChecksumMismatch, walletID.String())
	}

	return &wallet, nil
}

func (w *WalletActor) findLedger(walletID, key uuid.UUID) (*models.Ledger, bool, error) {
	var ledger models.Ledger
	err := w.db.
		Select("id", "initial_balance", "updated_balance").
		Where("wallet_id = ? AND idempotency_key = ?", walletID, key).
		Limit(1).
		Find(&ledger).Error
	if err != nil {
		return nil, false, err
	}
	if ledger.ID == 0 {
		return nil, false, nil
	}
	return &ledger, true, nil
}

// persist applies the balance change and writes the ledger row atomically.
// The UPDATE is guarded by the version read in load(); zero rows affected
// means something else changed the wallet and the caller re-reads and retries.
func (w *WalletActor) persist(wallet *models.Wallet, action models.LedgerType, txID, key uuid.UUID, amount int64, purpose string) (applyResult, error) {
	delta := amount
	if action == models.LedgerTypeDebit {
		delta = -amount
	}

	next := *wallet
	next.AvailableBalance += delta
	next.ActualBalance += delta
	next.Version++
	next.Checksum = ComputeChecksum(&next)

	if next.AvailableBalance < 0 || next.ActualBalance < 0 {
		return applyResult{}, newWalletError(CodeInsufficientFunds, "balance would go negative")
	}

	err := w.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.Wallet{}).
			Where("id = ? AND version = ?", wallet.ID, wallet.Version).
			Updates(map[string]any{
				"available_balance": next.AvailableBalance,
				"actual_balance":    next.ActualBalance,
				"version":           next.Version,
				"checksum":          next.Checksum,
				"updated_at":        time.Now(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return newWalletError(CodeVersionConflict,
				fmt.Sprintf("wallet %s version %d is stale", wallet.ID, wallet.Version))
		}

		return tx.Create(&models.Ledger{
			WalletID:       wallet.ID,
			TransactionID:  txID,
			IdempotencyKey: &key,
			Type:           action,
			Purpose:        purpose,
			Amount:         amount,
			Currency:       wallet.Currency,
			InitialBalance: wallet.ActualBalance,
			UpdatedBalance: next.ActualBalance,
		}).Error
	})
	if err != nil {
		return applyResult{}, err
	}

	// Keep the in-memory copy current and drop any cached read of this wallet.
	next.UpdatedAt = time.Now()
	w.wallet = &next
	cache.Delete(cache.WalletKey(wallet.ID.String()), cache.WalletNumberKey(wallet.Number))

	return applyResult{initial: wallet.ActualBalance, updated: next.ActualBalance}, nil
}

// ComputeChecksum hashes the fields that define a wallet's financial state.
// Kept in sync with the SQL backfill in migration 000005.
func ComputeChecksum(w *models.Wallet) string {
	payload := fmt.Sprintf("%s:%d:%d:%d:%d",
		w.ID, w.ActualBalance, w.AvailableBalance, w.ProcessingBalance, w.Version)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
