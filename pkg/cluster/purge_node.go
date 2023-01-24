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
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
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
		_, oldLabel := labels[kubeadmconstants.LabelNodeRoleOldControlPlane]
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
		if rookVersion.LT(semver.MustParse("1.4.9")) {
			c.Log.Warnf("The Rook version used is %s and it is recommended to update the Rook version. \n"+
				"More info: https://kurl.sh/docs/install-with-kurl/managing-nodes#rook-ceph-cluster-prerequisites \n"+
				"Be aware that by managing nodes with this Rook version it may leave Ceph unhealthy.\n"+
				"If you add new nodes after please, check the Ceph status (kubectl -n rook-ceph exec deployment.apps/rook-ceph-operator -- ceph status) \n"+
				"Therefore, if you verify that Ceph status is unhealthy then: \n"+
				"- Stop the Rook operator (kubectl -n rook-ceph scale --replicas=0 deployment.apps/rook-ceph-operator) \n"+
				"- Delete the deployment for the failed mon (find out the Peding pod to be deleted with: kubectl -n rook-ceph get pod -l app=rook-ceph-mon) \n"+
				"- Edit the configmap rook-ceph-mon-endpoints and (carefully) remove the failed mon from the list (kubect -n rook-ceph edit configmaps rook-ceph-mon-endpoints) \n"+
				"- Start the Rook operator (kubectl -n rook-ceph scale --replicas=1 deployment.apps/rook-ceph-operator) \n "+
				"- Ensure that Ceph came back in a healthy state (kubectl -n rook-ceph exec deployment.apps/rook-ceph-operator -- ceph status) \n"+
				"- For further information see: https://community.replicated.com/t/managing-nodes-when-the-previous-rook-version-is-in-use-might-leave-ceph-in-an-unhealthy-state-where-mon-pods-are-not-rescheduled/1099", rookVersion)
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
	var clusterStatus k8s121ClusterStatus

	cm, err := c.Config.Client.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(ctx, kubeadmconstants.KubeadmConfigConfigMap, metav1.GetOptions{})
	if err != nil {
		return "", nil, errors.Wrap(err, "get kube-system kubeadm-config ConfigMap")
	}

	// For k8s 1.22, this field doesn't exist, so no update required
	if _, ok := cm.Data[clusterStatusConfigMapKey]; !ok {
		return "", nil, nil
	}

	clusterStatus = k8s121ClusterStatus{}

	if err := k8syaml.Unmarshal([]byte(cm.Data[clusterStatusConfigMapKey]), &clusterStatus); err != nil {
		return "", nil, errors.Wrap(err, "unmarshal kubeadm-config ClusterStatus")
	}

	if clusterStatus.APIEndpoints == nil {
		clusterStatus.APIEndpoints = map[string]k8s121APIEndpoint{}
	}
	apiEndpoint, found := clusterStatus.APIEndpoints[name]
	if found {
		ip = apiEndpoint.AdvertiseAddress
		delete(clusterStatus.APIEndpoints, name)
		clusterStatusYaml, err := yaml.Marshal(clusterStatus)
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

	for _, apiEndpoint := range clusterStatus.APIEndpoints {
		remainingIPs = append(remainingIPs, apiEndpoint.AdvertiseAddress)
	}

	return ip, remainingIPs, nil
}

type k8s121ClusterStatus struct {
	metav1.TypeMeta
	APIEndpoints map[string]k8s121APIEndpoint
}

type k8s121APIEndpoint struct {
	AdvertiseAddress string
	BindPort         int32
}
