package cluster

import (
	"github.com/blang/semver"
	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"github.com/replicatedhq/ekco/pkg/k8s"
	"go.uber.org/zap"
	"sync"
)

const (
	DefaultEtcKubernetesDir = "/etc/kubernetes"
)

var Rookv14 = semver.MustParse("1.4.0")
var Rookv19 = semver.MustParse("1.9.0")
var CephPacific = semver.MustParse("16.2.0")
var CephQuincy = semver.MustParse("17.2.0")

type Controller struct {
	Config       types.ControllerConfig
	SyncExecutor k8s.SyncExecutorInterface
	Log          *zap.SugaredLogger

	sync.Mutex
}

func NewController(config types.ControllerConfig, log *zap.SugaredLogger) *Controller {
	syncExecutor := k8s.NewSyncExecutor(config.Client.CoreV1(), config.ClientConfig)
	return &Controller{
		Config:       config,
		SyncExecutor: syncExecutor,
		Log:          log,
	}
}
