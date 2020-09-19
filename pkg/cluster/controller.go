package cluster

import (
	"sync"

	"github.com/blang/semver"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

const (
	DefaultEtcKubernetesDir = "/etc/kubernetes"
)

var Rookv14 = semver.MustParse("1.4.0")

type ControllerConfig struct {
	Client          kubernetes.Interface
	ClientConfig    *restclient.Config
	CephV1          *cephv1.CephV1Client
	CertificatesDir string
	RookVersion     semver.Version
}

type Controller struct {
	Config ControllerConfig
	Log    *zap.SugaredLogger

	sync.Mutex
}

func NewController(config ControllerConfig, log *zap.SugaredLogger) *Controller {
	return &Controller{Config: config, Log: log}
}
