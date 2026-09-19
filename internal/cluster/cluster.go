// Package cluster wires the Proto.Actor cluster: single-node automanaged for
// local runs, Kubernetes provider for multi-pod deployments.
package cluster

import (
	"fmt"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/asynkron/protoactor-go/cluster/clusterproviders/automanaged"
	"github.com/asynkron/protoactor-go/cluster/clusterproviders/k8s"
	"github.com/asynkron/protoactor-go/cluster/identitylookup/disthash"
	"github.com/asynkron/protoactor-go/remote"
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
)

const requestTimeout = 10 * time.Second

// New builds and starts a cluster member with the given kinds registered.
func New(cfg *config.Config, kinds []*cluster.Kind) (*cluster.Cluster, error) {
	system := actor.NewActorSystem()

	remoteConfig := remote.Configure(cfg.AdvertisedHost, cfg.ClusterPort)

	provider, err := newProvider(cfg)
	if err != nil {
		return nil, err
	}

	clusterConfig := cluster.Configure(
		cfg.ClusterName,
		provider,
		disthash.New(),
		remoteConfig,
		cluster.WithKinds(kinds...),
		cluster.WithRequestTimeout(requestTimeout),
	)

	c := cluster.New(system, clusterConfig)
	c.StartMember()
	logger.InfoLog.Printf("cluster %s member started (%s mode) on %s:%d",
		cfg.ClusterName, cfg.ClusterMode, cfg.AdvertisedHost, cfg.ClusterPort)
	return c, nil
}

func newProvider(cfg *config.Config) (cluster.ClusterProvider, error) {
	switch cfg.ClusterMode {
	case "cluster":
		// Discovers peers through pod labels (cluster.proto.actor/*) in the
		// pod's own namespace and dials them on ADVERTISED_HOST:CLUSTER_PORT.
		logger.InfoLog.Printf("cluster: kubernetes provider, namespace %s (from service account)", cfg.K8sNamespace)
		return k8s.New()
	default:
		// Each local instance needs its own discovery port, or two processes
		// on one machine silently form a single cluster.
		return automanaged.NewWithConfig(2*time.Second, cfg.AutomanagedPort,
			fmt.Sprintf("localhost:%d", cfg.AutomanagedPort)), nil
	}
}
