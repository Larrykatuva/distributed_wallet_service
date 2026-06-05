package cluster

import (
	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/katuva/wallet/internal/actors"
	"gorm.io/gorm"
)

func RegisterKinds(db *gorm.DB) []*cluster.Kind {
	walletProps := actor.PropsFromProducer(func() actor.Actor {
		return actors.NewTransactionActor(db)
	})

	transactionProps := actor.PropsFromProducer(func() actor.Actor {
		return actors.NewTransactionActor(db)
	})

	return []*cluster.Kind{
		cluster.NewKind("Wallet", walletProps),
		cluster.NewKind("Transaction", transactionProps),
	}
}
