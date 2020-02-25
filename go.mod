module github.com/replicatedhq/ekco

go 1.13

require (
	github.com/coreos/bbolt v1.3.3 // indirect
	github.com/coreos/etcd v3.3.13+incompatible
	github.com/pkg/errors v0.8.1
	github.com/rook/rook v1.0.3
	github.com/spf13/cobra v0.0.5
	github.com/spf13/viper v1.3.2
	go.etcd.io/bbolt v1.3.3 // indirect
	go.uber.org/zap v0.0.0-20180814183419-67bc79d13d15
	go.undefinedlabs.com/scopeagent v0.1.12
	k8s.io/api v0.15.10
	k8s.io/apimachinery v0.15.10
	k8s.io/client-go v0.15.10
	k8s.io/kubernetes v1.15.10
)

replace (
	k8s.io/api => k8s.io/api v0.15.10
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.15.10
	k8s.io/apimachinery => k8s.io/apimachinery v0.15.10
	k8s.io/apiserver => k8s.io/apiserver v0.15.10
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.15.10
	k8s.io/client-go => k8s.io/client-go v0.15.10
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.15.10
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.15.10
	k8s.io/code-generator => k8s.io/code-generator v0.15.10
	k8s.io/component-base => k8s.io/component-base v0.15.10
	k8s.io/cri-api => k8s.io/cri-api v0.15.10
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.15.10
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.15.10
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.15.10
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.15.10
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.15.10
	k8s.io/kubelet => k8s.io/kubelet v0.15.10
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.15.10
	k8s.io/metrics => k8s.io/metrics v0.15.10
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.15.10
)
