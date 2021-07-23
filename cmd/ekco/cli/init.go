package cli

import (
	"github.com/blang/semver"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

func initEKCOConfig(v *viper.Viper) (*ekcoops.Config, error) {
	config := &ekcoops.Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}

	if config.MinReadyMasterNodes < 1 {
		return config, errors.New("min_ready_master_nodes must be at least 1")
	}

	return config, nil
}

func initClusterController(config *ekcoops.Config, log *zap.SugaredLogger) (*cluster.Controller, error) {
	clientConfig, err := restclient.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "load kubernetes config")
	}

	client, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "initialize kubernetes client")
	}

	rookcephclient, err := cephv1.NewForConfig(clientConfig)
	if err != nil {
		return nil, errors.Wrap(err, "initialize ceph client")
	}

	rookVersion, err := semver.Parse(config.RookVersion)
	if err != nil {
		return nil, errors.Wrap(err, "parse rook version")
	}

	return cluster.NewController(cluster.ControllerConfig{
		Client:                      client,
		ClientConfig:                clientConfig,
		CephV1:                      rookcephclient,
		CertificatesDir:             config.CertificatesDir,
		RookVersion:                 rookVersion,
		RookPriorityClass:           config.RookPriorityClass,
		RotateCerts:                 config.RotateCerts,
		RotateCertsImage:            config.RotateCertsImage,
		RotateCertsNamespace:        config.RotateCertsNamespace,
		RotateCertsCheckInterval:    config.RotateCertsCheckInterval,
		RotateCertsTTL:              config.RotateCertsTTL,
		RegistryCertNamespace:       config.RegistryCertNamespace,
		RegistryCertSecret:          config.RegistryCertSecret,
		KurlProxyCertNamespace:      config.KurlProxyCertNamespace,
		KurlProxyCertSecret:         config.KurlProxyCertSecret,
		KotsadmKubeletCertNamespace: config.KotsadmKubeletCertNamespace,
		KotsadmKubeletCertSecret:    config.KotsadmKubeletCertSecret,
		EnableInternalLoadBalancer:  config.EnableInternalLoadBalancer,
		InternalLoadBalancerPort:    config.InternalLoadBalancerPort,
		HostTaskImage:               config.HostTaskImage,
		HostTaskNamespace:           config.HostTaskNamespace,
	}, log), nil
}
