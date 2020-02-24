package cluster

import (
	"sync"

	"github.com/replicatedhq/ekco/pkg/logger"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

const (
	DefaultEtcKubernetesDir = "/etc/kubernetes"
)

type ControllerConfig struct {
	Client          kubernetes.Interface
	ClientConfig    *restclient.Config
	CephV1          *cephv1.CephV1Client
	CertificatesDir string
}

type Controller struct {
	Config ControllerConfig
	Log    *logger.Logger

	sync.Mutex
}

func NewController(config ControllerConfig, log *logger.Logger) *Controller {
	return &Controller{Config: config, Log: log}
}
