package cluster

import (
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

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
	Client                                kubernetes.Interface
	ClientConfig                          *restclient.Config
	CephV1                                *cephv1.CephV1Client
	AlertManagerV1                        dynamic.NamespaceableResourceInterface
	PrometheusV1                          dynamic.NamespaceableResourceInterface
	CertificatesDir                       string
	RookVersion                           semver.Version
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
	ContourCertNamespace                  string
	ContourCertSecret                     string
	EnvoyCertSecret                       string
	HostTaskImage                         string
	HostTaskNamespace                     string
	EnableInternalLoadBalancer            bool
	InternalLoadBalancerHAProxyImage      string
	AutoApproveKubeletCertSigningRequests bool
}

type Controller struct {
	Config ControllerConfig
	Log    *zap.SugaredLogger

	sync.Mutex
}

func NewController(config ControllerConfig, log *zap.SugaredLogger) *Controller {
	return &Controller{Config: config, Log: log}
}
