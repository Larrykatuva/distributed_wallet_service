package actors

import (
	"fmt"
	"sync"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/models"
	"gorm.io/gorm"
)

// TransactionNextAction represents the current stage of the transaction lifecycle.
// The actor uses this to decide what to do after all wallet replies are collected.
type TransactionNextAction string

const (
	// Processing is the initial state — transfers are being dispatched to wallet actors.
	Processing TransactionNextAction = "processing"

	// StatusUpdate means all transfers completed successfully — ready to mark the transaction.
	StatusUpdate TransactionNextAction = "update"

	// Reversal means one or more transfers failed — successfully applied transfers
	// must be reversed before the transaction can be marked as failed.
	Reversal TransactionNextAction = "reversal"
)

// TransactionActor orchestrates a single financial transaction end-to-end.
// It fans out debit and credit messages to the relevant WalletActors, collects
// their replies, and finalises the transaction status in the database.
//
// One TransactionActor is spawned per transaction — it lives only for the
// duration of that transaction and stops itself after addStatus completes.
type TransactionActor struct {
	db            *gorm.DB
	transactionID uuid.UUID
	date          time.Time // used in WHERE clause to leverage partition pruning
	transfers     []TransferEntry
	action        TransactionNextAction
	cluster       *cluster.Cluster
}

// NewTransactionActor creates a new TransactionActor with only the db dependency.
// All transaction-specific data (ID, date, transfers) arrives via StartTransactionMsg
// so this constructor matches the cluster kind producer signature.
func NewTransactionActor(db *gorm.DB) *TransactionActor {
	return &TransactionActor{
		db:     db,
		action: Processing,
	}
}

// Receive is the actor's message handler. Messages are processed one at a time
// from the mailbox — no concurrent access to actor state.
func (t *TransactionActor) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {

	// StartTransactionMsg kicks off the transaction flow.
	// Sent by the service layer after the transaction row has been persisted as pending.
	case StartTransactionMsg:
		t.transactionID = msg.TransactionID
		t.date = msg.Date
		t.transfers = msg.Transfers
		t.cluster = cluster.GetCluster(ctx.ActorSystem())

		// Mark transaction as processing synchronously before firing transfers.
		// This ensures status progression is always: pending → processing → success/failed.
		if err := t.startTransaction(); err != nil {
			logger.ErrorLog.Println("startTransaction failed:", err)
			return
		}

		// Fan out all transfers to their respective wallet actors in parallel.
		t.fireTransfers(ctx)

	// WalletResultMsg is the reply from a WalletActor after processing a debit or credit.
	// Collect replies one by one — when all have arrived, finalise the transaction.
	case WalletResultMsg:
		t.auditTransaction(msg)

		if t.isDone() {
			if t.action == Reversal {
				// One or more transfers failed — reverse the ones that succeeded
				t.fireReversals(ctx)
			} else {
				// All transfers succeeded — trigger final status update
				ctx.Send(ctx.Self(), AddTransactionStatusMsg{})
			}
		}

	// AddTransactionStatusMsg is sent by the actor to itself once all wallet
	// replies have been collected and any reversals have been processed.
	case AddTransactionStatusMsg:
		if err := t.addStatus(); err != nil {
			fmt.Println("Transaction failed:", err)
		} else {
			fmt.Println("Transaction succeeded")
		}
	}
}

// fireTransfers dispatches all transfers to their respective wallet actors in parallel.
// Each transfer is sent in its own goroutine — wg.Wait() ensures all requests are
// dispatched before the method returns, but does NOT wait for wallet replies.
// Replies arrive asynchronously as WalletResultMsg messages.
func (t *TransactionActor) fireTransfers(ctx actor.Context) {
	var wg sync.WaitGroup

	for i := range t.transfers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			transfer := t.transfers[idx]
			walletPID := t.spawnWalletActor(transfer.WalletID)

			if transfer.Action == models.LedgerTypeCredit {
				ctx.Request(walletPID, CreditWalletMsg{
					WalletID:       transfer.WalletID,
					Amount:         transfer.Amount,
					Purpose:        transfer.Purpose,
					Action:         transfer.Action,
					TransactionID:  t.transactionID,
					IdempotencyKey: t.transfers[idx].IdempotencyKey,
				})
			} else {
				ctx.Request(walletPID, DebitWalletMsg{
					WalletID:       transfer.WalletID,
					Amount:         transfer.Amount,
					Purpose:        transfer.Purpose,
					Action:         transfer.Action,
					TransactionID:  t.transactionID,
					IdempotencyKey: t.transfers[idx].IdempotencyKey,
				})
			}
		}(i)
	}

	wg.Wait()
}

// fireReversals sends the opposite operation to each wallet that successfully
// applied a transfer. A successful debit is reversed with a credit and vice versa.
// Reversal idempotency keys are regenerated so the wallet actor treats them as
// new operations rather than duplicates.
func (t *TransactionActor) fireReversals(ctx actor.Context) {
	hasPending := false

	for i := range t.transfers {
		// Only reverse transfers that were successfully applied —
		// failed ones never changed the balance so nothing to undo
		if !t.transfers[i].Success {
			continue
		}

		t.transfers[i].Done = false // reset so isDone() waits for reversal replies
		hasPending = true

		walletPID := t.spawnWalletActor(t.transfers[i].WalletID)
		t.transfers[i].IdempotencyKey = uuid.New() // fresh key for the reversal

		if t.transfers[i].Action == models.LedgerTypeDebit {
			// Reverse a debit by crediting the wallet back
			ctx.Request(walletPID, CreditWalletMsg{
				WalletID:       t.transfers[i].WalletID,
				Amount:         t.transfers[i].Amount,
				Purpose:        "reversal of txn " + t.transactionID.String(),
				TransactionID:  t.transactionID,
				IdempotencyKey: t.transfers[i].IdempotencyKey,
			})
		} else {
			// Reverse a credit by debiting the wallet back
			ctx.Request(walletPID, DebitWalletMsg{
				WalletID:       t.transfers[i].WalletID,
				Amount:         t.transfers[i].Amount,
				Purpose:        "reversal of txn " + t.transactionID.String(),
				TransactionID:  t.transactionID,
				IdempotencyKey: t.transfers[i].IdempotencyKey,
			})
		}
	}

	// Edge case: nothing was successfully applied — skip straight to status update
	if !hasPending {
		ctx.Send(ctx.Self(), AddTransactionStatusMsg{})
	}
}

// startTransaction updates the transaction status from pending to processing.
// Called synchronously before transfers are dispatched to guarantee correct
// status ordering in the database.
func (t *TransactionActor) startTransaction() error {
	return t.db.
		Model(&models.Transaction{}).
		Where("id = ? AND created_at >= ?", t.transactionID, t.date).
		Updates(map[string]any{
			"status": string(models.TransactionStatusProcessing),
		}).Error
}

// addStatus writes the final transaction outcome to the database.
// Called after all wallet replies (and any reversals) have been collected.
// Marks the transaction as success or failed and sets the completion timestamp.
func (t *TransactionActor) addStatus() error {
	// Collect the first transfer error — determines final status
	var firstErr error
	for _, transfer := range t.transfers {
		if !transfer.Success && transfer.Error != nil {
			firstErr = transfer.Error
			break
		}
	}

	status := models.TransactionStatusSuccess
	message := "Transaction processed successfully"

	if firstErr != nil || t.action == Reversal {
		status = models.TransactionStatusFailed
		if firstErr != nil {
			message = firstErr.Error()
		} else {
			message = "Transaction reversed"
		}
	}

	return t.db.
		Model(&models.Transaction{}).
		Where("id = ? AND created_at >= ?", t.transactionID, t.date).
		Updates(map[string]any{
			"status":         string(status),
			"narration":      message,
			"date_completed": time.Now(),
			"is_completed":   true,
		}).Error
}

// auditTransaction records the result of a single wallet operation.
// Matches the reply to its transfer entry by idempotency key and marks it done.
// If the reply contains an error, the action is escalated to Reversal so that
// all successfully applied transfers are unwound after isDone() returns true.
func (t *TransactionActor) auditTransaction(msg WalletResultMsg) {
	for i := range t.transfers {
		if t.transfers[i].IdempotencyKey != msg.IdempotencyKey {
			continue
		}

		t.transfers[i].Done = true

		if msg.Error != nil {
			t.transfers[i].Success = false
			t.transfers[i].Error = msg.Error
			t.action = Reversal // escalate — will trigger fireReversals after isDone
		} else {
			t.transfers[i].Success = true
		}
	}
}

// isDone returns true when all transfers have received a reply from their
// respective wallet actors. Used to determine when to finalise the transaction.
func (t *TransactionActor) isDone() bool {
	for i := range t.transfers {
		if !t.transfers[i].Done {
			return false
		}
	}
	return true
}

// spawnWalletActor resolves the PID for a wallet actor via the cluster.
// The cluster uses disthash to ensure the same wallet ID always routes to
// the same node, serialising all operations for that wallet.
// Falls back to a local spawn if the cluster has not yet placed the actor.
func (t *TransactionActor) spawnWalletActor(walletID uuid.UUID) *actor.PID {
	walletPID := t.cluster.Get(walletID.String(), "Wallet")

	if walletPID == nil {
		// Cluster not ready or kind not yet placed — spawn locally as fallback
		props := actor.PropsFromProducer(func() actor.Actor {
			return NewWalletActor(t.db, walletID)
		})
		walletPID = t.cluster.ActorSystem.Root.Spawn(props)
	}

	return walletPID
}

// SpawnTransactionActor resolves or spawns a TransactionActor for the given
// transaction ID. Called by the service layer to get the PID before sending
// StartTransactionMsg. Uses the cluster for distributed placement.
func SpawnTransactionActor(db *gorm.DB, cluster *cluster.Cluster, transactionId uuid.UUID) *actor.PID {
	txnPID := cluster.Get(transactionId.String(), "Transaction")

	if txnPID == nil {
		// Cluster not ready — spawn locally as fallback
		props := actor.PropsFromProducer(func() actor.Actor {
			return NewTransactionActor(db)
		})
		txnPID = cluster.ActorSystem.Root.Spawn(props)
	}

	return txnPID
}
