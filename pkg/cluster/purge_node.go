package cluster

import (
	"context"
	"fmt"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

const (
	clusterStatusConfigMapKey = "ClusterStatus"
)

// PurgeNode cleans up a lost node.
func (c *Controller) PurgeNode(ctx context.Context, name string, rook bool, rookVersion *semver.Version) error {
	c.Log.Infof("Purge node %q", name)

	// get the Node before deleting because the etcd peer member removal step below may need the IP
	node, err := c.Config.Client.CoreV1().Nodes().Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if util.FilterOutReasonNotFoundErr(err) != nil {
			return errors.Wrap(err, "get Node")
		}
		node = nil
	}

	if rook && rookVersion != nil {
		err := c.purgeCephOsd(ctx, *rookVersion, name)
		if err != nil {
			c.Log.Warnf("Purge node %q: ceph osd purge command failed with error: %v", name, err)
		}
	}

	maybeMaster := true
	if node != nil {
		labels := node.ObjectMeta.GetLabels()

		// Note that the latest version no longer has the following label
		// TODO: remove this const when we be able to no longer provide support/use old kubedmin versions
		// Keep the label here allow the latest ekco versions works with old KURL releases
		// LabelNodeRoleOldControlPlane specifies that a node hosts control-plane components
		// DEPRECATED: https://github.com/kubernetes/kubeadm/issues/2200
		const LabelNodeRoleOldControlPlane = "node-role.kubernetes.io/master"

		_, oldLabel := labels[LabelNodeRoleOldControlPlane]
		_, newLabel := labels[kubeadmconstants.LabelNodeRoleControlPlane]
		if !oldLabel && !newLabel {
			maybeMaster = false
		}
	}

	if maybeMaster {
		var ip string
		var remainingIPs []string

		ip, remainingIPs, err = c.removeKubeadmEndpoint(ctx, name)
		if err != nil {
			return err
		}
		if ip != "" {
			c.Log.Infof("Purge node %q: kubeadm-config API endpoint removed", name)
		}

		// if we couldn't grab the IPs of the other API servers, collect them from active pod labels
		if remainingIPs == nil {
			remainingIPs, err = c.getEndpointIPsFromPods(ctx)
			if err != nil {
				return errors.Wrap(err, "could not get cluster endpoints from pods")
			}
		}

		// get etcd peer URL for purged node if it wasn't in kubeadm's ClusterStatus
		if ip == "" && node != nil {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					ip = addr.Address
					c.Log.Debugf("Purge node %q: got ip from Node", name)
					break
				}
			}
		}

		// remove etcd member
		if ip != "" {
			if err := c.removeEtcdPeer(ip, remainingIPs); err != nil {
				return err
			}
		}
	}

	if node != nil {
		if err := c.deleteK8sNode(ctx, name); err != nil {
			return err
		}
		c.Log.Infof("Purge node %q: deleted Kubernetes Node object", name)

		// The following error cannot be faced in upper versions.
		// We are adding here the steps to fix it manually.
		// More info: https://github.com/rook/rook/issues/2262#issuecomment-460898915
		if rookVersion != nil {
			if rookVersion.LT(semver.MustParse("1.4.9")) {
				c.Log.Warnf("The Rook version used is %s and it is recommended to update the Rook version. \n"+
					"More info: https://kurl.sh/docs/install-with-kurl/managing-nodes#rook-ceph-cluster-prerequisites \n"+
					"It's worth noting that using this version of Rook to manage nodes may result in an unhealthy Ceph cluster.\n"+
					"If new nodes are added, it is recommended to check the status of Ceph (using the command 'kubectl -n rook-ceph exec deployment.apps/rook-ceph-operator -- ceph status'). \n"+
					"If Ceph is found to be unhealthy, please check the topic: \n"+
					"https://community.replicated.com/t/managing-nodes-when-the-previous-rook-version-is-in-use-might-leave-ceph-in-an-unhealthy-state-where-mon-pods-are-not-rescheduled/1099", rookVersion)
			}
		}
	}

	return nil
}

// purgeCephOsd safely removes the OSD on a particular node name from the Ceph cluster.
func (c *Controller) purgeCephOsd(ctx context.Context, rookVersion semver.Version, name string) error {
	if err := c.removeCephClusterStorageNode(name); err != nil {
		return err
	}

	osdID, err := c.deleteK8sDeploymentOSD(name)
	if err != nil {
		return err
	}

	if osdID != "" {
		if err := c.execCephOSDPurge(rookVersion, osdID, name); err != nil {
			return err
		}
		c.Log.Infof("Purge node %q: ceph osd purge command executed", name)
	}

	return nil
}

// getEndpointIPsFromPods returns the IP endpoints of the API server nodes from the pods in the cluster.
// For kURL clusers, these also run embedded ETCD pods. Based on the logic in kubeadm:
// https://github.com/kubernetes/kubernetes/blob/master/cmd/kubeadm/app/util/config/cluster.go#L229
func (c *Controller) getEndpointIPsFromPods(ctx context.Context) ([]string, error) {
	var endpoints []string

	podList, err := c.Config.Client.CoreV1().Pods(metav1.NamespaceSystem).List(
		ctx,
		metav1.ListOptions{
			LabelSelector: fmt.Sprintf("component=%s,tier=%s", kubeadmconstants.KubeAPIServer, kubeadmconstants.ControlPlaneTier),
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "could not retrieve list of pods to determine api server endpoints")
	}

	for _, pod := range podList.Items {
		if rawEndpoint, ok := pod.Annotations[kubeadmconstants.KubeAPIServerAdvertiseAddressEndpointAnnotationKey]; ok {
			parseEndpoint, err := kubeadmapi.APIEndpointFromString(rawEndpoint)
			if err != nil {
				return nil, errors.Wrap(err, "could not parse api server endpoint from pod annotation")
			}
			endpoints = append(endpoints, parseEndpoint.AdvertiseAddress)
		}
	}

	return endpoints, nil
}

func (c *Controller) deleteK8sNode(ctx context.Context, name string) error {
	err := c.Config.Client.CoreV1().Nodes().Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return errors.Wrapf(err, "delete Kubernetes Node object %q", name)
	}

	return nil
}

// removeKubeadmEndpoint removes the IP from the Legacy ClusterStatus object which was removed in K8S 1.22.
// Returns the removed ip if found, plus the remaining ips.
func (c *Controller) removeKubeadmEndpoint(ctx context.Context, name string) (string, []string, error) {
	var ip string
	var remainingIPs []string

	cm, err := c.Config.Client.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(ctx, kubeadmconstants.KubeadmConfigConfigMap, metav1.GetOptions{})
	if err != nil {
		return "", nil, errors.Wrap(err, "get kube-system kubeadm-config ConfigMap")
	}

	// For k8s 1.22, this field doesn't exist, so no update required
	if _, ok := cm.Data[clusterStatusConfigMapKey]; !ok {
		return "", nil, nil
	}

	clusterStatus, err := unmarshalClusterStatus([]byte(cm.Data[clusterStatusConfigMapKey]))
	if err != nil {
		return "", nil, err
	}

	apiEndpoints := clusterStatus.apiEndpoints()
	if apiEndpoints == nil {
		apiEndpoints = map[string]k8s121APIEndpoint{}
	}
	endpoint, found := apiEndpoints[name]
	if found {
		ip = endpoint.advertiseAddress()
		delete(apiEndpoints, name)
		clusterStatusYaml, err := marshalClusterStatus(clusterStatus)
		if err != nil {
			return "", nil, err
		}

		cm.Data[clusterStatusConfigMapKey] = string(clusterStatusYaml)
		_, err = c.Config.Client.CoreV1().ConfigMaps(metav1.NamespaceSystem).Update(ctx, cm, metav1.UpdateOptions{})
		if err != nil {
			return "", nil, errors.Wrap(err, "update kube-system kubeadm-config ConfigMap")
		}
		c.Log.Infof("Purge node %q: kubeadm-config API endpoint removed", name)
	}

	for _, endpoint := range apiEndpoints {
		remainingIPs = append(remainingIPs, endpoint.advertiseAddress())
	}

	return ip, remainingIPs, nil
}

// unmarshalClusterStatus takes a raw kubeadm.k8s.io/v1beta2 ClusterStatus config yaml
// and converts it to a k8s121ClusterStatus object
// NB: A previous commit (https://github.com/replicatedhq/ekco/commit/f3884ddafbde034e3b46d0c9a5b96b9a797cba6a)
// introduced a bug where the kubeadm ClusterStatus config was being written as:
// ---
// apiendpoints:
//
//	rafael-kurl-ecko-purge-master:
//	  advertiseaddress: 10.128.0.126
//	  bindport: 6443
//	rafael-kurl-ecko-purge-master-2:
//	  advertiseaddress: 10.128.0.63
//	  bindport: 6443
//
// typemeta:
//
//	apiversion: kubeadm.k8s.io/v1beta2
//	kind: ClusterStatus
//
// unmarshalClusterStatus() will still decode the aforementioned erroneous YAML document
func unmarshalClusterStatus(data []byte) (*k8s121ClusterStatus, error) {
	clusterStatus := k8s121ClusterStatus{}
	if err := yaml.Unmarshal([]byte(data), &clusterStatus); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal kubeadm-config ClusterStatus")
	}
	return &clusterStatus, nil
}

// marshalClusterStatus takes a k8s121ClusterStatus object and converts it to a raw
// kubeadm.k8s.io/v1beta2 ClusterStatus config YAML document
// An example ClusterStatus config YAML:
// ---
// apiEndpoints:
//
//	rafael-kurl-ecko-purge-master:
//	  advertiseAddress: 10.128.0.126
//	  bindPort: 6443
//	rafael-kurl-ecko-purge-master-2:
//	  advertiseAddress: 10.128.0.63
//	  bindPort: 6443
//
// apiVersion: kubeadm.k8s.io/v1beta2
// kind: ClusterStatus
func marshalClusterStatus(clusterStatus *k8s121ClusterStatus) ([]byte, error) {
	// deepcopy map
	apiEndpointsCopy := make(map[string]k8s121APIEndpoint)
	csApiEndpoints := clusterStatus.apiEndpoints()
	apiVersion := clusterStatus.apiVersion()
	kind := clusterStatus.kind()

	for k, v := range csApiEndpoints {
		apiEndpointsCopy[k] = k8s121APIEndpoint{
			AdvertiseAddress: v.advertiseAddress(),
			BindPort:         v.bindPort(),
		}
	}

	clusterStatusToWrite := k8s121ClusterStatus{
		Kind:         kind,
		APIVersion:   apiVersion,
		APIEndpoints: apiEndpointsCopy,
	}

	clusterStatusYaml, err := yaml.Marshal(&clusterStatusToWrite)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal kubeadm-config ClusterStatus")
	}
	return clusterStatusYaml, nil
}

// k8s121ClusterStatus represents a kubeadm.k8s.io/v1beta2 ClusterStatus config
// It is used to decode the following YAML documents:
// ---
// apiendpoints:
//
//	rafael-kurl-ecko-purge-master:
//	  advertiseaddress: 10.128.0.126
//	  bindport: 6443
//	rafael-kurl-ecko-purge-master-2:
//	  advertiseaddress: 10.128.0.63
//	  bindport: 6443
//
// typemeta:
//
//	apiversion: kubeadm.k8s.io/v1beta2
//	kind: ClusterStatus
//
// ---
// apiEndpoints:
//
//	rafael-kurl-ecko-purge-master:
//	  advertiseAddress: 10.128.0.126
//	  bindPort: 6443
//	rafael-kurl-ecko-purge-master-2:
//	  advertiseAddress: 10.128.0.63
//	  bindPort: 6443
//
// apiVersion: kubeadm.k8s.io/v1beta2
// kind: ClusterStatus
type k8s121ClusterStatus struct {
	metav1.TypeMeta `yaml:",omitempty"`
	Kind            string
	APIVersion      string                       `yaml:"apiVersion"`
	APIEndpoints    map[string]k8s121APIEndpoint `yaml:"apiEndpoints"`
	APIEndpointsLC  map[string]k8s121APIEndpoint `yaml:"apiendpoints,omitempty"`
}

func (k k8s121ClusterStatus) kind() string {
	kind := k.Kind
	if k.TypeMeta.Kind != "" {
		kind = k.TypeMeta.Kind
	}
	return kind
}

func (k k8s121ClusterStatus) apiVersion() string {
	apiVersion := k.APIVersion
	if k.TypeMeta.APIVersion != "" {
		apiVersion = k.TypeMeta.APIVersion
	}
	return apiVersion
}

func (k k8s121ClusterStatus) apiEndpoints() map[string]k8s121APIEndpoint {
	endpoints := k.APIEndpoints
	if k.APIEndpointsLC != nil {
		endpoints = k.APIEndpointsLC
	}
	return endpoints
}

type k8s121APIEndpoint struct {
	AdvertiseAddress   string `yaml:"advertiseAddress"`
	AdvertiseAddressLC string `yaml:"advertiseaddress,omitempty"`
	BindPort           int32  `yaml:"bindPort"`
	BindPortLC         int32  `yaml:"bindport,omitempty"`
}

func (k k8s121APIEndpoint) advertiseAddress() string {
	advertiseAddress := k.AdvertiseAddress
	if k.AdvertiseAddressLC != "" {
		advertiseAddress = k.AdvertiseAddressLC
	}
	return advertiseAddress
}

func (k k8s121APIEndpoint) bindPort() int32 {
	port := k.BindPort
	if k.BindPortLC != 0 {
		port = k.BindPortLC
	}
	return port
}
