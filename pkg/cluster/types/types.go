package types

import (
	"github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	v12 "github.com/vmware-tanzu/velero/pkg/generated/clientset/versioned/typed/velero/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"time"
)

type ControllerConfig struct {
	Client                                kubernetes.Interface
	ClientConfig                          *rest.Config
	CephV1                                v1.CephV1Interface
	AlertManagerV1                        dynamic.NamespaceableResourceInterface
	PrometheusV1                          dynamic.NamespaceableResourceInterface
	VeleroV1                              *v12.VeleroV1Client
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
