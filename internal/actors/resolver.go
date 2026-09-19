package actors

import (
	"errors"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
)

// WalletResolver returns the PID that owns a wallet's mailbox. The production
// implementation asks the cluster so the same wallet always routes to the same
// node.
type WalletResolver interface {
	WalletPID(ctx actor.Context, walletID uuid.UUID) (*actor.PID, error)
}

var errClusterUnavailable = errors.New("cluster could not place wallet actor")

// ClusterResolver resolves wallet grains through the Proto.Actor cluster that
// owns the calling actor system. It is safe to construct before the cluster
// exists because the lookup happens at call time.
type ClusterResolver struct{}

func (ClusterResolver) WalletPID(ctx actor.Context, walletID uuid.UUID) (*actor.PID, error) {
	c := cluster.GetCluster(ctx.ActorSystem())
	if c == nil {
		return nil, errClusterUnavailable
	}
	pid := c.Get(walletID.String(), KindWallet)
	if pid == nil {
		return nil, errClusterUnavailable
	}
	return pid, nil
}
