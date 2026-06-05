package actors

import (
	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/katuva/wallet/dpk/logger"
	"gorm.io/gorm"
)

type DeadLatterActor struct {
	db      *gorm.DB
	cluster *cluster.Cluster
}

func NewDeadLatterActor(db *gorm.DB, cluster *cluster.Cluster) *DeadLatterActor {
	return &DeadLatterActor{
		db:      db,
		cluster: cluster,
	}
}

func (dl *DeadLatterActor) RegisterHandler() {
	system := dl.cluster.ActorSystem

	system.EventStream.Subscribe(func(evt interface{}) {
		if l, ok := evt.(*actor.DeadLetterEvent); ok {
			switch msg := l.Message.(type) {

			case StartTransactionMsg:
				// Transaction never started — safe to retry or mark failed
				logger.ErrorLog.Printf(
					"DEAD LETTER: StartTransactionMsg lost txn_id=%s — mark failed or retry",
					msg.TransactionID,
				)

			case DebitWalletMsg:
				// Debit never reached wallet actor — balance unchanged, safe to retry
				logger.ErrorLog.Printf(
					"DEAD LETTER: DebitWalletMsg lost txn_id=%s wallet_id=%s amount=%v",
					msg.TransactionID, msg.WalletID, msg.Amount,
				)

			case CreditWalletMsg:
				// Credit never reached wallet actor — balance unchanged, safe to retry
				logger.ErrorLog.Printf(
					"DEAD LETTER: CreditWalletMsg lost txn_id=%s wallet_id=%s amount=%v",
					msg.TransactionID, msg.WalletID, msg.Amount,
				)

			case WalletResultMsg:
				// Reply from wallet never reached transaction actor
				// may have crashed after sending debit/credit
				// This is the dangerous case — wallet balance may have changed
				// but transaction actor never recorded it
				logger.ErrorLog.Printf(
					"DEAD LETTER: WalletResultMsg lost txn_id=%s wallet_id=%s — RECONCILIATION REQUIRED",
					msg.TransactionID, msg.WalletID,
				)

			case AddTransactionStatusMsg:
				// Final status update lost — transaction stuck in processing
				logger.ErrorLog.Printf(
					"DEAD LETTER: AddTransactionStatusMsg lost — transaction may be stuck in processing",
				)
			}
		}
	})
}
