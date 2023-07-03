package types

import (
	"time"

	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/generated/clientset/versioned/typed/velero/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ControllerConfig struct {
	Client                                kubernetes.Interface
	ClientConfig                          *rest.Config
	CephV1                                cephv1.CephV1Interface
	AlertManagerV1                        dynamic.NamespaceableResourceInterface
	PrometheusV1                          dynamic.NamespaceableResourceInterface
	VeleroV1                              velerov1.VeleroV1Interface
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
	RookCephImage                         string
}
