package actors

import (
	"regexp"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/logger"
	pb "github.com/katuva/wallet/proto/actors"
)

// Recoverer re-drives a transaction whose orchestration was interrupted.
// Implemented by the recovery manager; kept as an interface to avoid a cycle.
type Recoverer interface {
	Recover(transactionID uuid.UUID, reason string)
}

// uuidPattern extracts the grain identity from a cluster PID such as
// "partition-activator/<uuid>$abc".
var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// RegisterDeadLetterHandler resolves undeliverable actor messages instead of
// only logging them:
//
//   - a wallet op that never reached its actor is answered on the sender's
//     behalf with an "undeliverable" result, so the orchestrator fails the leg
//     and reverses the others;
//   - a lost start, lost reply, lost timeout or lost finalize hands the
//     transaction to the recovery manager, which re-drives it from the
//     persisted plan and the ledger.
func RegisterDeadLetterHandler(system *actor.ActorSystem, recoverer Recoverer) {
	system.EventStream.Subscribe(func(evt any) {
		dl, ok := evt.(*actor.DeadLetterEvent)
		if !ok {
			return
		}

		switch msg := dl.Message.(type) {
		case *pb.StartTransactionMsg:
			logger.ErrorLog.Printf("DEAD LETTER: StartTransactionMsg txn=%s undeliverable; scheduling recovery", msg.TransactionId)
			recoverByID(recoverer, msg.TransactionId, "start message undeliverable")

		case *pb.DebitWalletMsg:
			logger.ErrorLog.Printf("DEAD LETTER: DebitWalletMsg txn=%s wallet=%s amount=%d undeliverable (balance unchanged)",
				msg.TransactionId, msg.WalletId, msg.Amount)
			answerUndeliverable(system, dl.Sender, recoverer, msg.TransactionId, msg.WalletId, msg.IdempotencyKey, msg.Amount)

		case *pb.CreditWalletMsg:
			logger.ErrorLog.Printf("DEAD LETTER: CreditWalletMsg txn=%s wallet=%s amount=%d undeliverable (balance unchanged)",
				msg.TransactionId, msg.WalletId, msg.Amount)
			answerUndeliverable(system, dl.Sender, recoverer, msg.TransactionId, msg.WalletId, msg.IdempotencyKey, msg.Amount)

		case *pb.WalletResultMsg:
			// The wallet may have moved but the orchestrator is gone: rebuild
			// its state from the ledger and continue.
			logger.ErrorLog.Printf("DEAD LETTER: WalletResultMsg txn=%s wallet=%s key=%s code=%q lost; scheduling recovery",
				msg.TransactionId, msg.WalletId, msg.IdempotencyKey, msg.ErrorCode)
			recoverByID(recoverer, msg.TransactionId, "wallet reply lost")

		case *pb.TransactionTimeoutMsg:
			id := msg.TransactionId
			if id == "" {
				id = identityFromPID(dl.PID)
			}
			logger.WarningLog.Printf("DEAD LETTER: TransactionTimeoutMsg txn=%s for a stopped actor; scheduling recovery", id)
			recoverByID(recoverer, id, "timeout message undeliverable")

		case *pb.FinalizeTransactionMsg:
			id := msg.TransactionId
			if id == "" {
				id = identityFromPID(dl.PID)
			}
			logger.ErrorLog.Printf("DEAD LETTER: FinalizeTransactionMsg txn=%s lost; scheduling recovery", id)
			recoverByID(recoverer, id, "finalize message undeliverable")
		}
	})
}

// answerUndeliverable replies to the orchestrator as the wallet would have,
// with an error, so the transaction fails cleanly instead of hanging until
// its timeout. Without a sender it falls back to full recovery.
func answerUndeliverable(system *actor.ActorSystem, sender *actor.PID, recoverer Recoverer, txID, walletID, key string, amount int64) {
	if sender == nil {
		recoverByID(recoverer, txID, "wallet message undeliverable, no sender")
		return
	}
	system.Root.Send(sender, &pb.WalletResultMsg{
		TransactionId:  txID,
		WalletId:       walletID,
		IdempotencyKey: key,
		Amount:         amount,
		ErrorCode:      string(CodeUndeliverable),
		ErrorMessage:   "wallet actor unreachable",
	})
}

func recoverByID(recoverer Recoverer, id, reason string) {
	if recoverer == nil {
		return
	}
	txID, err := uuid.Parse(id)
	if err != nil {
		logger.ErrorLog.Printf("DEAD LETTER: cannot recover, bad transaction id %q", id)
		return
	}
	recoverer.Recover(txID, reason)
}

func identityFromPID(pid *actor.PID) string {
	if pid == nil {
		return ""
	}
	return uuidPattern.FindString(pid.Id)
}
