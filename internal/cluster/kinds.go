package cluster

import (
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/katuva/wallet/internal/actors"
	"gorm.io/gorm"
)

// Kinds builds the grain kinds the cluster can activate. Grain identity is
// the wallet or transaction UUID, which disthash maps to a single node.
func Kinds(db *gorm.DB, notifier actors.Notifier, replyTimeout time.Duration) []*cluster.Kind {
	walletProps := actor.PropsFromProducer(func() actor.Actor {
		return actors.NewWalletActor(db)
	})

	transactionProps := actor.PropsFromProducer(func() actor.Actor {
		return actors.NewTransactionActor(db, actors.ClusterResolver{}, notifier).(*actors.TransactionActor).WithReplyTimeout(replyTimeout)
	})

	return []*cluster.Kind{
		cluster.NewKind(actors.KindWallet, walletProps),
		cluster.NewKind(actors.KindTransaction, transactionProps),
	}
}
