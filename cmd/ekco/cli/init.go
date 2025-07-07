package cli

import (
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	"github.com/spf13/viper"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func init() {
	utilruntime.Must(velerov1.AddToScheme(scheme.Scheme))
}

func initEKCOConfig(v *viper.Viper) (*ekcoops.Config, error) {
	config := &ekcoops.Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}

	if config.MinReadyMasterNodes < 1 {
		return config, errors.New("min_ready_master_nodes must be at least 1")
	}

	if !v.IsSet("contour_namespace") && v.IsSet("contour_cert_namespace") {
		config.ContourNamespace = config.ContourCertNamespace
	}

	return config, nil
}

func initClusterController(config *ekcoops.Config, log *zap.SugaredLogger) (*cluster.Controller, error) {
	clientConfig, err := restclient.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "load kubernetes config")
	}

	kclient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "initialize kubernetes client")
	}

	ctrlClient, err := client.New(clientConfig, client.Options{})
	if err != nil {
		return nil, errors.Wrap(err, "initialize controller runtime client")
	}

	rookcephclient, err := cephv1.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "initialize ceph client")
	}

	dynamicClient, err := dynamic.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating Kubernetes client.")
	}

	prometheusClient := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "prometheuses",
	})
	alertManagerClient := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "alertmanagers",
	})

	return cluster.NewController(types.ControllerConfig{
		ClientConfig:                          clientConfig,
		Client:                                kclient,
		CtrlClient:                            ctrlClient,
		CephV1:                                rookcephclient,
		CertificatesDir:                       config.CertificatesDir,
		AlertManagerV1:                        alertManagerClient,
		PrometheusV1:                          prometheusClient,
		RookPriorityClass:                     config.RookPriorityClass,
		RotateCerts:                           config.RotateCerts,
		RotateCertsImage:                      config.RotateCertsImage,
		RotateCertsNamespace:                  config.RotateCertsNamespace,
		RotateCertsCheckInterval:              config.RotateCertsCheckInterval,
		RotateCertsTTL:                        config.RotateCertsTTL,
		RegistryCertNamespace:                 config.RegistryCertNamespace,
		RegistryCertSecret:                    config.RegistryCertSecret,
		KurlProxyCertNamespace:                config.KurlProxyCertNamespace,
		KurlProxyCertSecret:                   config.KurlProxyCertSecret,
		KotsadmKubeletCertNamespace:           config.KotsadmKubeletCertNamespace,
		KotsadmKubeletCertSecret:              config.KotsadmKubeletCertSecret,
		ContourNamespace:                      config.ContourNamespace,
		ContourCertSecret:                     config.ContourCertSecret,
		EnvoyCertSecret:                       config.EnvoyCertSecret,
		RestartFailedEnvoyPods:                config.RestartFailedEnvoyPods,
		EnvoyPodsNotReadyDuration:             config.EnvoyPodsNotReadyDuration,
		EnableInternalLoadBalancer:            config.EnableInternalLoadBalancer,
		InternalLoadBalancerHAProxyImage:      config.InternalLoadBalancerHAProxyImage,
		HostTaskImage:                         config.HostTaskImage,
		HostTaskNamespace:                     config.HostTaskNamespace,
		AutoApproveKubeletCertSigningRequests: config.AutoApproveKubeletCertSigningRequests,
		RookCephImage:                         config.RookCephImage,
	}, log), nil
}
