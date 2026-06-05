package cluster

import (
	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/asynkron/protoactor-go/cluster/clusterproviders/automanaged"
	"github.com/asynkron/protoactor-go/cluster/clusterproviders/k8s"
	"github.com/asynkron/protoactor-go/cluster/identitylookup/disthash"
	"github.com/asynkron/protoactor-go/remote"
	"github.com/katuva/wallet/config"
	"github.com/katuva/wallet/dpk/logger"
)

var instance *cluster.Cluster

type ClusterClient interface {
	Shutdown(graceful bool)
	StartMember()
}

func Init(cfg *config.Config) {
	if instance != nil {
		logger.InfoLog.Println("Cluster already initialized")
		return
	}

	system := actor.NewActorSystem()

	remoteConfig := remote.Configure(
		cfg.AdvertisedHost,
		cfg.ClusterPort,
	)

	lookup := disthash.New()

	clusterConfig := cluster.Configure(
		cfg.ClusterName,
		newProvider(cfg),
		lookup,
		remoteConfig,
	)

	instance = cluster.New(system, clusterConfig)
	logger.InfoLog.Println("Cluster initialized")
}

func Get() *cluster.Cluster {
	if instance == nil {
		panic("cluster not initialized — call Init first")
	}
	return instance
}

func Start() {
	Get().StartMember()
	logger.InfoLog.Println("Cluster member started")
}

func Shutdown() {
	if instance == nil {
		logger.InfoLog.Println("Cluster not running, nothing to shutdown")
		return
	}
	instance.Shutdown(true)
	instance = nil
	logger.InfoLog.Println("Cluster shutdown complete")
}

func newProvider(cfg *config.Config) cluster.ClusterProvider {
	switch cfg.ClusterMode {
	case "cluster":
		logger.InfoLog.Println("Starting in distributed cluster mode (k8s)")
		provider, err := k8s.New()
		if err != nil {
			logger.ErrorLog.Fatalf("Failed to create k8s provider: %v", err)
		}
		return provider

	default:
		logger.InfoLog.Println("Starting in single node mode (automanaged)")
		return automanaged.New()
	}
}
