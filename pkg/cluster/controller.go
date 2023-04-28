package cluster

import (
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/blang/semver"
	"github.com/replicatedhq/ekco/pkg/k8s"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	veleroclientv1 "github.com/vmware-tanzu/velero/pkg/generated/clientset/versioned/typed/velero/v1"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

const (
	DefaultEtcKubernetesDir = "/etc/kubernetes"
)

var Rookv14 = semver.MustParse("1.4.0")
var Rookv19 = semver.MustParse("1.9.0")
var CephPacific = semver.MustParse("16.2.0")
var CephQuincy = semver.MustParse("17.2.0")

type ControllerConfig struct {
	Client                                kubernetes.Interface
	ClientConfig                          *restclient.Config
	CephV1                                cephv1.CephV1Interface
	AlertManagerV1                        dynamic.NamespaceableResourceInterface
	PrometheusV1                          dynamic.NamespaceableResourceInterface
	VeleroV1                              *veleroclientv1.VeleroV1Client
	CertificatesDir                       string
	RookPriorityClass                     string
	RotateCerts                           bool
	RotateCertsImage                      string
	RotateCertsNamespace                  string
	RotateCertsCheckInterval              time.Duration
	RotateCertsTTL                        time.Duration
	RegistryCertNamespace                 string
	RegistryCertSecret                    string
	KurlProxyCertNamespace                string
	KurlProxyCertSecret                   string
	KotsadmKubeletCertNamespace           string
	KotsadmKubeletCertSecret              string
	ContourNamespace                      string
	ContourCertSecret                     string
	EnvoyCertSecret                       string
	RestartFailedEnvoyPods                bool
	EnvoyPodsNotReadyDuration             time.Duration
	HostTaskImage                         string
	HostTaskNamespace                     string
	EnableInternalLoadBalancer            bool
	InternalLoadBalancerHAProxyImage      string
	AutoApproveKubeletCertSigningRequests bool
}

type Controller struct {
	Config       ControllerConfig
	SyncExecutor k8s.SyncExecutorInterface
	Log          *zap.SugaredLogger

	sync.Mutex
}

func NewController(config ControllerConfig, log *zap.SugaredLogger) *Controller {
	syncExecutor := k8s.NewSyncExecutor(config.Client.CoreV1(), config.ClientConfig)
	return &Controller{
		Config:       config,
		SyncExecutor: syncExecutor,
		Log:          log,
	}
}
