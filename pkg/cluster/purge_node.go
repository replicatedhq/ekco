package cluster

import (
	"context"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	configutil "k8s.io/kubernetes/cmd/kubeadm/app/util/config"
)

// PurgeNode cleans up a lost node.
func (c *Controller) PurgeNode(ctx context.Context, name string, rook bool) error {
	c.Log.Debug("Purge node %q", name)

	// get the Node before deleting because the etcd peer member removal step below may need the IP
	node, err := c.Config.Client.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	if err != nil {
		if util.FilterOutReasonNotFoundErr(err) != nil {
			return errors.Wrap(err, "get Node")
		}
		node = nil
	}

	if rook {
		if err := c.removeCephClusterStorageNode(name); err != nil {
			return err
		}

		osdID, err := c.deleteK8sDeploymentOSD(name)
		if err != nil {
			return err
		}

		if osdID != "" {
			if err := c.execCephOSDPurge(osdID); err != nil {
				return err
			}
			c.Log.Debug("Purge node %q: ceph osd purge command executed", name)
		}
	}

	maybeMaster := true
	if node != nil {
		labels := node.ObjectMeta.GetLabels()
		if _, ok := labels[kubeadmconstants.LabelNodeRoleMaster]; !ok {
			maybeMaster = false
		}
	}

	if maybeMaster {
		var ip string
		var remainingIPs []string

		ip, remainingIPs, err = c.removeKubeadmEndpoint(name)
		if err != nil {
			return err
		}
		if ip != "" {
			c.Log.Debug("Purge node %q: kubeadm-config API endpoint removed", name)
		}

		// get etcd peer URL for purged node if it wasn't in kubeadm's ClusterStatus
		if ip == "" && node != nil {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					ip = addr.Address
					c.Log.Debug("Purge node %q: got ip from Node", name)
					break
				}
			}
		}

		// remove etcd member
		if ip != "" {
			if err := c.removeEtcdPeer(ctx, ip, remainingIPs); err != nil {
				return err
			}
		}
	}

	if err := c.removeDaemonBindToPurgedNode(name); err != nil {
		return errors.Wrap(err, "remove daemon bind to purged node")
	}

	if node != nil {
		if err := c.deleteK8sNode(ctx, name); err != nil {
			return err
		}
		c.Log.Debug("Purge node %q: deleted Kubernetes Node object", name)
	}

	return nil
}

func (c *Controller) deleteK8sNode(ctx context.Context, name string) error {
	err := c.Config.Client.CoreV1().
		Nodes().
		Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return errors.Wrapf(err, "delete Kubernetes Node object %q", name)
	}

	return nil
}

// returns the removed ip if found, plus the remaining ips
func (c *Controller) removeKubeadmEndpoint(name string) (string, []string, error) {
	var ip string
	var remainingIPs []string

	clusterStatus, err := configutil.GetClusterStatus(c.Config.Client)
	if err != nil {
		return "", nil, errors.Wrap(err, "get kube-system kubeadm-config ConfigMap ClusterStatus")
	}
	if clusterStatus.APIEndpoints == nil {
		clusterStatus.APIEndpoints = map[string]kubeadmapi.APIEndpoint{}
	}
	apiEndpoint, found := clusterStatus.APIEndpoints[name]
	if found {
		ip = apiEndpoint.AdvertiseAddress
		delete(clusterStatus.APIEndpoints, name)
		clusterStatusYaml, err := configutil.MarshalKubeadmConfigObject(clusterStatus)
		if err != nil {
			return "", nil, err
		}
		cm, err := c.Config.Client.CoreV1().ConfigMaps("kube-system").Get(kubeadmconstants.KubeadmConfigConfigMap, metav1.GetOptions{})
		if err != nil {
			return "", nil, errors.Wrap(err, "get kube-system kubeadm-config ConfigMap")
		}
		cm.Data[kubeadmconstants.ClusterStatusConfigMapKey] = string(clusterStatusYaml)
		_, err = c.Config.Client.CoreV1().ConfigMaps("kube-system").Update(cm)
		if err != nil {
			return "", nil, errors.Wrap(err, "update kube-system kubeadm-config ConfigMap")
		}
		c.Log.Debug("Purge node %q: kubeadm-config API endpoint removed", name)
	}

	for _, apiEndpoint := range clusterStatus.APIEndpoints {
		remainingIPs = append(remainingIPs, apiEndpoint.AdvertiseAddress)
	}

	return ip, remainingIPs, nil
}

// On airgap install the daemon is bound to the first master to avoid rescheduling before the hourly
// job to replicate airgap bundle and license to all masters has completed.
func (c *Controller) removeDaemonBindToPurgedNode(hostname string) error {
	deploy, err := c.Config.Client.AppsV1().Deployments("default").Get("replicated", metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "get replicated deployment")
	}
	if deploy.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"] == hostname {
		delete(deploy.Spec.Template.Spec.NodeSelector, "kubernetes.io/hostname")
		deploy.Spec.Template.Spec.NodeSelector["node-role.kubernetes.io/master"] = ""

		_, err := c.Config.Client.AppsV1().Deployments("default").Update(deploy)
		return errors.Wrap(err, "update replicated deployment")
	}
	return nil
}
