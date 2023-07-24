package cluster

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/helm"
	"github.com/replicatedhq/ekco/pkg/helm/charts"
	"github.com/replicatedhq/ekco/pkg/helm/rookcephcluster"
	"github.com/replicatedhq/ekco/pkg/k8s"
	"github.com/replicatedhq/ekco/pkg/util"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/strings/slices"
)

const (
	// maxMonCount is the maximum number of mon replicas that should be
	// deployed to a cluster
	maxMonCount = 3

	// minMonCount is the minimum number of mon replicas that should be
	// deployed to a cluster
	minMonCount = 1

	// maxMgrCount is the maximum number of mgr replicas that should be
	// deployed to a cluster
	maxMgrCount = 2

	// minMgrCount is the minimum number of mgr replicas that should be
	// deployed to a cluster
	minMgrCount = 1
)

var cephErrENOENT = errors.New("Ceph ENOENT")
var cephOSDStatusRX = regexp.MustCompile(`^\s*\d\s+(?P<host>\S+)`)

//go:embed filesystem-multinode.yaml
var filesystemMultinodeYAML []byte

var filesystemMultinodeJSON []byte

func init() {
	var err error
	filesystemMultinodeJSON, err = yaml.ToJSON(filesystemMultinodeYAML)
	if err != nil {
		panic(fmt.Sprintf("Failed to convert filesystem-multinode.yaml to json: %v", err))
	}
}

// returns the number of nodes used for storage, which may be higher than the number of names passed in
// if a node is currently not ready but has not been purged
func (c *Controller) UseNodesForStorage(ctx context.Context, rookVersion semver.Version, cluster *cephv1.CephCluster, names []string, manageNodes bool) (int, error) {
	// do not manage nodes if the user is managing them
	if !manageNodes {
		var next []cephv1.Node
		storageNodes := make(map[string]bool, len(cluster.Spec.Storage.Nodes))
		for _, storageNode := range cluster.Spec.Storage.Nodes {
			next = append(next, storageNode)
			storageNodes[storageNode.Name] = true
		}
		changed := false
		for _, name := range names {
			if !storageNodes[name] {
				c.Log.Infof("Adding node %q to CephCluster node storage list", name)
				next = append(next, cephv1.Node{
					Name: name,
				})
				changed = true
			}
		}
		if changed {
			patches := []k8s.JSONPatchOperation{}
			patches = append(patches, k8s.JSONPatchOperation{
				Op:    k8s.JSONPatchOpReplace,
				Path:  "/spec/storage/nodes",
				Value: next,
			})
			patches = append(patches, k8s.JSONPatchOperation{
				Op:    k8s.JSONPatchOpReplace,
				Path:  "/spec/storage/useAllNodes",
				Value: false,
			})

			_, err := c.JSONPatchCephCluster(ctx, patches)
			if err != nil {
				return 0, errors.Wrap(err, "patch CephCluster with new storage node list")
			}
		}
	} else {
		c.Log.Debugf("EKCO is not managing CephCluster storage nodes")
	}

	return c.countUniqueHostsWithOSD(ctx, rookVersion)
}

func (c *Controller) removeCephClusterStorageNode(ctx context.Context, name string) error {
	cluster, err := c.GetCephCluster(ctx)
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}
	changed := false
	var next []cephv1.Node
	for _, node := range cluster.Spec.Storage.Nodes {
		if node.Name == name {
			c.Log.Infof("Removing node %q from CephCluster storage list", name)
			changed = true
		} else {
			next = append(next, node)
		}
	}

	if changed {
		patches := []k8s.JSONPatchOperation{}
		patches = append(patches, k8s.JSONPatchOperation{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/storage/nodes",
			Value: next,
		})
		_, err = c.JSONPatchCephCluster(ctx, patches)
		if err != nil {
			return errors.Wrap(err, "patch CephCluster with new storage node list")
		}

		c.Log.Infof("Purge node %q: removed from CephCluster node storage list", name)
	}

	return nil
}

// returns the osd id if found
func (c *Controller) deleteK8sDeploymentOSD(ctx context.Context, name string) (string, error) {
	var osdID string

	rookOSDLabels := map[string]string{"app": "rook-ceph-osd"}
	opts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(rookOSDLabels).String(),
	}
	deploys, err := c.Config.Client.AppsV1().Deployments(RookCephNS).List(ctx, opts)
	if err != nil {
		return "", errors.Wrap(err, "list Rook OSD deployments")
	}
	for _, deploy := range deploys.Items {
		hostname := deploy.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]
		if hostname == name {
			labels := deploy.ObjectMeta.GetLabels()
			osdID = labels["ceph-osd-id"]
			background := metav1.DeletePropagationBackground
			opts := metav1.DeleteOptions{
				PropagationPolicy: &background,
			}
			err := c.Config.Client.AppsV1().Deployments(RookCephNS).Delete(ctx, deploy.Name, opts)
			if err != nil {
				return "", errors.Wrapf(err, "delete deployment %s", deploy.Name)
			}
			c.Log.Infof("Deleted OSD Deployment for node %s", name)
			break
		}
	}

	return osdID, nil
}

// getBlockPoolReplicationLevel returns ceph block pool replication size
func (c *Controller) GetBlockPoolReplicationLevel(ctx context.Context, name string) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("name of CephBlockPool required")
	}

	pool, err := c.Config.CephV1.CephBlockPools(RookCephNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return 0, nil
		}
		return 0, errors.Wrapf(err, "failed to get CephBlockPool %s", name)
	}

	return int(pool.Spec.Replicated.Size), nil
}

// SetBlockPoolReplicationLevel ignores NotFound errors.
func (c *Controller) SetBlockPoolReplication(ctx context.Context, rookVersion semver.Version, cephVersion *semver.Version, name string, level int, doFullReconcile bool) (bool, error) {
	if name == "" {
		return false, nil
	}

	pool, err := c.Config.CephV1.CephBlockPools(RookCephNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "get CephBlockPool %s", name)
	}
	current := int(pool.Spec.Replicated.Size)
	if !(current < level || doFullReconcile) {
		return false, nil
	}

	if current != level {
		c.Log.Infof("Changing CephBlockPool replication level from %d to %d", current, level)
	} else {
		c.Log.Debugf("Ensuring CephBlockPool replication level is %d", level)
	}
	patches := []k8s.JSONPatchOperation{{
		Op:    k8s.JSONPatchOpReplace,
		Path:  "/spec/replicated/size",
		Value: uint(level),
	}}
	patchData, err := json.Marshal(patches)
	if err != nil {
		return false, errors.Wrap(err, "marshal patch data")
	}
	c.Log.Debugf("Patching CephBlockPool %s with %s", pool.Name, string(patchData))
	_, err = c.Config.CephV1.CephBlockPools(RookCephNS).Patch(ctx, pool.Name, apitypes.JSONPatchType, patchData, metav1.PatchOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "patch CephBlockPool %s", name)
	}
	if rookVersion.LT(Rookv14) {
		// Changing the replicated size of the pool in the CephBlockPool does not set the min_size on
		// the pool. The min_size remains at 1, which allows I/O in a degraded state and can lead to
		// data loss. https://github.com/rook/rook/issues/4718
		minSize := 1
		if level > 1 {
			minSize = 2
		}
		// Rook will also run this command but there is a race condition: if the next command that
		// sets the min_size runs before Rook has increased the size, then the min_size command will
		// fail. This command is idempotent.
		err = c.cephOSDPoolSetSize(ctx, rookVersion, cephVersion, name, level)
		if err != nil {
			return false, errors.Wrapf(err, "set block pool %q size to %d", name, minSize)
		}
		err = c.cephOSDPoolSetMinSize(ctx, rookVersion, name, minSize)
		if err != nil {
			return false, errors.Wrapf(err, "set block pool %q min_size to %d", name, minSize)
		}
	}
	return true, nil
}

func (c *Controller) SetDeviceHealthMetricsReplication(ctx context.Context, rookVersion semver.Version, cephVersion *semver.Version, cephBlockPoolName string, level int, doFullReconcile bool) (bool, error) {
	if rookVersion.LT(Rookv14) {
		return false, nil
	}

	// There is no CR to compare the desired and current level.
	if !doFullReconcile {
		return false, nil
	}

	minSize := 1
	if level > 1 {
		minSize = 2
	}

	poolName := CephDeviceHealthMetricsPool
	if cephVersion != nil && cephVersion.Major >= CephQuincy.Major {
		poolName = CephDeviceHealthMetricsPoolQuincy
	}

	c.Log.Debugf("Ensuring %s replication level is %d", poolName, level)

	err := c.cephOSDPoolSetSize(ctx, rookVersion, cephVersion, poolName, level)
	if err != nil {
		return false, errors.Wrapf(err, "scale %s pool size", poolName)
	}
	err = c.cephOSDPoolSetMinSize(ctx, rookVersion, poolName, minSize)
	if err != nil {
		return false, errors.Wrapf(err, "scale %s pool min_size", poolName)
	}

	return true, nil
}

// ReconcileMonCount ensures the CephCluster has the desired number of mons.
// A single mon for clusters with 1 or 2 nodes, and 3 mons for all other
// clusters.
func (c *Controller) ReconcileMonCount(ctx context.Context, nodeCount int) error {
	// single mon for 1 or 2 node cluster, 3 mons for all other clusters
	desiredMonCount := maxMonCount
	if nodeCount < maxMonCount {
		desiredMonCount = minMonCount
	}

	cluster, err := c.GetCephCluster(ctx)
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}

	if cluster.Spec.Mon.Count == desiredMonCount {
		return nil
	}
	if cluster.Spec.Mon.Count > desiredMonCount {
		c.Log.Debugf("Will not reduce mon count from %s to %s", cluster.Spec.Mon.Count, desiredMonCount)
		return nil
	}

	c.Log.Infof("Increasing mon count from %d to %d", cluster.Spec.Mon.Count, desiredMonCount)

	patches := []k8s.JSONPatchOperation{{
		Op:    k8s.JSONPatchOpReplace,
		Path:  "/spec/mon/count",
		Value: desiredMonCount,
	}}

	_, err = c.JSONPatchCephCluster(ctx, patches)
	if err != nil {
		return errors.Wrap(err, "patch CephCluster with new mon count")
	}

	return nil
}

// ReconcileMgrCount ensures the CephCluster has the desired number of mgrs.
// A single mgr for clusters with 1 node, and 2 mgrs for all other clusters.
func (c *Controller) ReconcileMgrCount(ctx context.Context, rookVersion semver.Version, nodeCount int) error {
	if rookVersion.LT(Rookv19) {
		return nil
	}

	// single mgr for 1 node cluster, 2 mgrs for all other clusters
	desiredMgrCount := maxMgrCount
	if nodeCount < maxMgrCount {
		desiredMgrCount = minMgrCount
	}

	cluster, err := c.GetCephCluster(ctx)
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}

	if cluster.Spec.Mgr.Count == desiredMgrCount {
		return nil
	}
	if cluster.Spec.Mgr.Count > desiredMgrCount {
		c.Log.Debugf("Will not reduce mgr count from %s to %s", cluster.Spec.Mgr.Count, desiredMgrCount)
		return nil
	}

	c.Log.Infof("Increasing mgr count from %d to %d", cluster.Spec.Mgr.Count, desiredMgrCount)

	patches := []k8s.JSONPatchOperation{{
		Op:    k8s.JSONPatchOpReplace,
		Path:  "/spec/mgr/count",
		Value: desiredMgrCount,
	}}

	_, err = c.JSONPatchCephCluster(ctx, patches)
	if err != nil {
		return errors.Wrap(err, "patch CephCluster with new mgr count")
	}

	return nil
}

const (
	cephCSIRbdProvisionerResource    = "- name : csi-provisioner\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-resizer\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-attacher\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-snapshotter\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-rbdplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n- name : csi-omap-generator\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n- name : liveness-prometheus\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n"
	cephCSIRbdPluginResource         = "- name : driver-registrar\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n- name : csi-rbdplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n- name : liveness-prometheus\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n"
	cephCSICephfsProvisionerResource = "- name : csi-provisioner\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-resizer\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-attacher\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-snapshotter\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-cephfsplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n- name : liveness-prometheus\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n"
	cephCSICephfsPluginResource      = "- name : driver-registrar\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n- name : csi-cephfsplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n- name : liveness-prometheus\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n"
	cephCSINfsProvisionerResource    = "- name : csi-provisioner\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 100m\n    limits:\n      memory: 256Mi\n      cpu: 200m\n- name : csi-nfsplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n"
	cephCSINfsPluginResource         = "- name : driver-registrar\n  resource:\n    requests:\n      memory: 128Mi\n      cpu: 50m\n    limits:\n      memory: 256Mi\n      cpu: 100m\n- name : csi-nfsplugin\n  resource:\n    requests:\n      memory: 512Mi\n      cpu: 250m\n    limits:\n      memory: 1Gi\n      cpu: 500m\n"
)

var (
	cephCSIResourcesPatch = []byte(fmt.Sprintf(
		`{"data":{`+
			`"CSI_RBD_PROVISIONER_RESOURCE":"%s",`+
			`"CSI_RBD_PLUGIN_RESOURCE":"%s",`+
			`"CSI_CEPHFS_PROVISIONER_RESOURCE":"%s",`+
			`"CSI_CEPHFS_PLUGIN_RESOURCE":"%s",`+
			`"CSI_NFS_PROVISIONER_RESOURCE":"%s",`+
			`"CSI_NFS_PLUGIN_RESOURCE":"%s"`+
			`}}`,
		strings.ReplaceAll(cephCSIRbdProvisionerResource, "\n", "\\n"),
		strings.ReplaceAll(cephCSIRbdPluginResource, "\n", "\\n"),
		strings.ReplaceAll(cephCSICephfsProvisionerResource, "\n", "\\n"),
		strings.ReplaceAll(cephCSICephfsPluginResource, "\n", "\\n"),
		strings.ReplaceAll(cephCSINfsProvisionerResource, "\n", "\\n"),
		strings.ReplaceAll(cephCSINfsPluginResource, "\n", "\\n"),
	))
)

// SetCephCSIResources will set CSI provisioner and plugin resources to their recommendations once
// the cluster has enough capacity at 3 nodes.
func (c *Controller) SetCephCSIResources(ctx context.Context, rookVersion semver.Version, nodeCount int) (bool, error) {
	if rookVersion.LT(Rookv19) {
		return false, nil
	}

	if nodeCount < 3 {
		return false, nil
	}

	configMap, err := c.Config.Client.CoreV1().ConfigMaps(RookCephNS).Get(ctx, "rook-ceph-operator-config", metav1.GetOptions{})
	if err != nil {
		return false, errors.Wrap(err, "get rook-ceph-operator-config configmap")
	}

	if !cephCSIResourcesNeedsUpdate(configMap.Data) {
		return false, nil
	}

	c.Log.Infof("Setting Ceph CSI plugin and provisioner resources")

	_, err = c.Config.Client.CoreV1().ConfigMaps(RookCephNS).Patch(ctx, "rook-ceph-operator-config", apitypes.MergePatchType, cephCSIResourcesPatch, metav1.PatchOptions{})
	if err != nil {
		return false, errors.Wrap(err, "patch rook-ceph-operator-config configmap")
	}
	return true, nil
}

func cephCSIResourcesNeedsUpdate(data map[string]string) bool {
	return data["CSI_RBD_PROVISIONER_RESOURCE"] != cephCSIRbdProvisionerResource ||
		data["CSI_RBD_PLUGIN_RESOURCE"] != cephCSIRbdPluginResource ||
		data["CSI_CEPHFS_PROVISIONER_RESOURCE"] != cephCSICephfsProvisionerResource ||
		data["CSI_CEPHFS_PLUGIN_RESOURCE"] != cephCSICephfsPluginResource ||
		data["CSI_NFS_PROVISIONER_RESOURCE"] != cephCSINfsProvisionerResource ||
		data["CSI_NFS_PLUGIN_RESOURCE"] != cephCSINfsPluginResource
}

// SetSharedFilesystemReplication will set the shared filesystem replication to
// the number of OSDs in the cluster. Returns true if the resource was updated.
func (c *Controller) SetFilesystemReplication(ctx context.Context, rookVersion semver.Version, cephVersion *semver.Version, name string, level int, doFullReconcile bool) (bool, error) {
	if name == "" {
		return false, nil
	}

	cephFilesystem, err := c.Config.CephV1.CephFilesystems(RookCephNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "get Filesystem %s", name)
	}
	patches := []k8s.JSONPatchOperation{}
	for i, pool := range cephFilesystem.Spec.DataPools {
		current := int(pool.Replicated.Size)
		if current < level {
			cephFilesystem.Spec.DataPools[i].Replicated.Size = uint(level)
			patches = append(patches, k8s.JSONPatchOperation{
				Op:    k8s.JSONPatchOpReplace,
				Path:  fmt.Sprintf("/spec/dataPools/%d/replicated/size", i),
				Value: uint(level),
			})
		}
	}
	current := int(cephFilesystem.Spec.MetadataPool.Replicated.Size)
	if current < level {
		cephFilesystem.Spec.MetadataPool.Replicated.Size = uint(level)
		patches = append(patches, k8s.JSONPatchOperation{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/metadataPool/replicated/size",
			Value: uint(level),
		})
	}

	if !(len(patches) > 0 || doFullReconcile) {
		return false, nil
	}

	if len(patches) > 0 {
		c.Log.Infof("Changing CephFilesystem pool replication level from %d to %d", current, level)
	} else {
		c.Log.Debugf("Ensuring CephFilesystem pool replication level is %d", level)
	}

	patchData, err := json.Marshal(patches)
	if err != nil {
		return false, errors.Wrap(err, "json marshal patches")
	}

	c.Log.Debugf("Patching CephFilesystem %s with %s", cephFilesystem.Name, string(patchData))
	_, err = c.Config.CephV1.CephFilesystems(RookCephNS).Patch(ctx, cephFilesystem.Name, apitypes.JSONPatchType, patchData, metav1.PatchOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "patch Filesystem %s", name)
	}
	if rookVersion.LT(Rookv14) {
		minSize := 1
		if level > 1 {
			minSize = 2
		}
		// Changing the size in the CephFilesystem has no effect so it needs to be set manually
		// https://github.com/rook/rook/issues/3144
		err := c.cephOSDPoolSetSize(ctx, rookVersion, cephVersion, RookCephSharedFSMetadataPool, level)
		if err != nil {
			return false, errors.Wrapf(err, "scale shared cephFS metadata pool size")
		}
		err = c.cephOSDPoolSetMinSize(ctx, rookVersion, RookCephSharedFSMetadataPool, minSize)
		if err != nil {
			return false, errors.Wrapf(err, "scale shared cephFS metadata pool min_size")
		}
		err = c.cephOSDPoolSetSize(ctx, rookVersion, cephVersion, RookCephSharedFSDataPool, level)
		if err != nil {
			return false, errors.Wrapf(err, "scale shared cephFS data pool size")
		}
		err = c.cephOSDPoolSetMinSize(ctx, rookVersion, RookCephSharedFSDataPool, minSize)
		if err != nil {
			return false, errors.Wrapf(err, "scale shared cephFS data pool min_size")
		}
	}

	return true, nil
}

// PatchFilesystemMDSPlacementMultinode will change the patched
// preferredDuringSchedulingIgnoredDuringExecution podAntiAffinity rule back to the more correct
// requiredDuringSchedulingIgnoredDuringExecution equivalent if the number of nodes is greater than
// one.
func (c *Controller) PatchFilesystemMDSPlacementMultinode(ctx context.Context, name string, numNodes int) error {
	if name == "" {
		return nil
	}

	if numNodes <= 1 {
		return nil
	}

	c.Log.Debugf("Ensuring CephFilesystem %s patched for multi-node installation", name)

	previous, err := c.Config.CephV1.CephFilesystems(RookCephNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil
		}
		return errors.Wrapf(err, "get cephfilesystem %s", name)
	}

	// If this installation has previously been patched by kURL for single node mds support
	placement := previous.Spec.MetadataServer.Placement
	if placement.PodAntiAffinity != nil && len(placement.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution) == 2 {
		result, err := c.Config.CephV1.CephFilesystems(RookCephNS).Patch(ctx, name, apitypes.MergePatchType, filesystemMultinodeJSON, metav1.PatchOptions{})
		if err != nil {
			return errors.Wrapf(err, "patch cephfilesystem %s", name)
		}

		if result.ObjectMeta.Generation > previous.ObjectMeta.Generation {
			c.Log.Infof("CephFilesystem %s patched for multi-node installation", name)
		} else {
			c.Log.Debugf("CephFilesystem %s unchanged by patch", name)
		}
	}
	return nil
}

// SetObjectStoreReplication ignores NotFound errors.

// SetObjectStoreReplication will set the object store pool replication to the
// number of OSDs in the cluster. Returns true if the resource was updated.
func (c *Controller) SetObjectStoreReplication(ctx context.Context, rookVersion semver.Version, cephVersion *semver.Version, name string, level int, doFullReconcile bool) (bool, error) {
	if name == "" {
		return false, nil
	}

	os, err := c.Config.CephV1.CephObjectStores(RookCephNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "get CephObjectStore %s", name)
	}

	patches := []k8s.JSONPatchOperation{}

	current := int(os.Spec.DataPool.Replicated.Size)
	if current < level {
		patches = append(patches, k8s.JSONPatchOperation{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/dataPool/replicated/size",
			Value: uint(level),
		})
	}

	current = int(os.Spec.MetadataPool.Replicated.Size)
	if current < level {
		patches = append(patches, k8s.JSONPatchOperation{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/metadataPool/replicated/size",
			Value: uint(level),
		})
	}

	if !(len(patches) > 0 || doFullReconcile) {
		return false, nil
	}

	minSize := 1
	if level > 1 {
		minSize = 2
	}
	if len(patches) > 0 {
		c.Log.Infof("Changing CephObjectStore pool replication level from %d to %d", current, level)
	} else {
		c.Log.Debugf("Ensuring CephOjbectStore pool replication level is %d", level)
	}

	patchData, err := json.Marshal(patches)
	if err != nil {
		return false, errors.Wrap(err, "json marshal patches")
	}

	c.Log.Debugf("Patching CephObjectStore %s with %s", os.Name, string(patchData))
	_, err = c.Config.CephV1.CephObjectStores(RookCephNS).Patch(ctx, os.Name, apitypes.JSONPatchType, patchData, metav1.PatchOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "patch CephObjectStore %s", name)
	}

	// Changing the size in the CephObjectStore has no effect in Rook 1.0 so it needs to be set
	// manually https://github.com/rook/rook/issues/4341
	if rookVersion.LT(Rookv14) {
		pools := append([]string{RookCephObjectStoreRootPool}, RookCephObjectStoreMetadataPools...)
		pools = append(pools, RookCephObjectStoreDataPools...)
		for _, pool := range pools {
			err := c.cephOSDPoolSetSize(ctx, rookVersion, cephVersion, objectStorePoolName(name, pool), level)
			if err == cephErrENOENT && slices.Contains(RookCephObjectStoreMetadataPools, pool) {
				// the non-ec metadata pool doesn't always exist
				continue
			}
			if err != nil {
				return false, errors.Wrapf(err, "scale ceph object store pool %s size", pool)
			}
			err = c.cephOSDPoolSetMinSize(ctx, rookVersion, objectStorePoolName(name, pool), minSize)
			if err != nil {
				return false, errors.Wrapf(err, "scale ceph object store pool %s min_size", pool)
			}
		}
	}

	// Object store pools pg_num_min is incorrectly set to 32 for Ceph Quincy. Tell the autoscaler
	// to reduce the PGs to 8. https://github.com/rook/rook/issues/11366
	if cephVersion != nil && cephVersion.Major >= CephQuincy.Major {
		var multiErr error
		for _, pool := range append([]string{RookCephObjectStoreRootPool}, RookCephObjectStoreMetadataPoolsQuincy...) {
			err := c.cephOSDPoolSetPgNumMin(ctx, rookVersion, objectStorePoolName(name, pool), 8)
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set ceph object store metadata pool %s pg_num_min", pool))
			}
		}
		if multiErr != nil {
			return false, multiErr
		}
	}

	return true, nil
}

// Toolbox is deployed with Rook 1.4 since ceph commands can't be executed in operator
func (c *Controller) rookCephExec(ctx context.Context, rookVersion semver.Version, cmd ...string) error {
	container, rookLabels := c.rookCephExecTarget(rookVersion)
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods(RookCephNS).List(ctx, opts)
	if err != nil {
		return errors.Wrap(err, "list Rook pods")
	}
	if len(pods.Items) == 0 {
		return errors.Wrap(err, "found no Rook pods for executing ceph commands")
	}

	exitCode, stdout, stderr, err := c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, cmd...)
	if err != nil {
		return err
	}
	if exitCode == 2 {
		c.Log.Debugf("Rook ceph exec %q exited with code %d and stderr: %s", cmd, exitCode, stderr)
		return cephErrENOENT
	}
	if exitCode != 0 {
		c.Log.Infof("Rook ceph exec %q exited with code %d and stderr: %s", cmd, exitCode, stderr)

		return fmt.Errorf("exec %q: %d", cmd, exitCode)
	}

	c.Log.Debugf("Exec Rook ceph %q exited with code %d and stdout: %s", cmd, exitCode, stdout)

	return nil
}

func (c *Controller) execCephOSDPurge(ctx context.Context, rookVersion semver.Version, osdID string, hostname string) error {
	container, rookLabels := c.rookCephExecTarget(rookVersion)
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods(RookCephNS).List(ctx, opts)
	if err != nil {
		return errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) == 0 {
		return errors.Wrapf(err, "found no Rook pods for executing ceph commands")
	}
	// ignore error - OSD is probably already down
	_, _, _, _ = c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, "ceph", "osd", "down", osdID)

	exitCode, stdout, stderr, err := c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, "ceph", "osd", "purge", osdID, "--yes-i-really-mean-it")
	if exitCode != 0 {
		c.Log.Debugf("`ceph osd purge %s` stdout: %s", osdID, stdout)
		return fmt.Errorf("failed to purge OSD: %s", stderr)
	}
	if err != nil {
		return errors.Wrap(err, "ceph osd purge")
	}

	// This removes the phantom OSD from the output of `ceph osd tree`
	exitCode, stdout, stderr, err = c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, "ceph", "osd", "crush", "rm", hostname)
	if exitCode != 0 {
		c.Log.Debugf("`ceph osd crush rm %s` stdout: %s", hostname, stdout)
		return fmt.Errorf("failed to rm %s from crush map: %s", hostname, stderr)
	}
	if err != nil {
		return errors.Wrap(err, "ceph osd purge")
	}

	return nil
}

func (c *Controller) CephFilesystemOK(ctx context.Context, rookVersion semver.Version, name string) (bool, error) {
	container, rookLabels := c.rookCephExecTarget(rookVersion)
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods(RookCephNS).List(ctx, opts)
	if err != nil {
		return false, errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) != 1 {
		return false, errors.Wrapf(err, "found %d Rook tools pods", len(pods.Items))
	}
	// The filesystem will appear in `ceph fs ls` before it's ready to use. `ceph mds metadata` is
	// better because it waits for the mds daemons to be running
	exitCode, stdout, stderr, err := c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, "ceph", "mds", "metadata")
	if err != nil {
		return false, errors.Wrap(err, "running 'ceph fs ls'")
	}
	if exitCode != 0 {
		c.Log.Debugf("`ceph fs ls` stdout: %s", stdout)
		return false, fmt.Errorf("failed to list ceph filesystems: %s", stderr)
	}

	mdsA := fmt.Sprintf("%s-a", name)
	mdsB := fmt.Sprintf("%s-b", name)

	return strings.Contains(stdout, mdsA) && strings.Contains(stdout, mdsB), nil
}

func (c *Controller) WaitCephFilesystem(ctx context.Context, rookVersion semver.Version, name string) error {
	ok, err := c.CephFilesystemOK(ctx, rookVersion, name)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			ok, err := c.CephFilesystemOK(ctx, rookVersion, name)
			if err != nil {
				return err
			}
			if ok {
				// There were two cases during testing where the shared fs mount failed after
				// returning immediately at this point so add sleep to be safe.
				time.Sleep(time.Second * 10)
				return nil
			}
		}
	}
}

func objectStorePoolName(storeName, poolName string) string {
	if strings.HasPrefix(poolName, ".") {
		return poolName
	}
	// the name of the pool is <instance>.<name>, except for the pool ".rgw.root" that spans object stores
	return fmt.Sprintf("%s.%s", storeName, poolName)
}

// returns labelSelector, containerName
func (c *Controller) rookCephExecTarget(rookVersion semver.Version) (string, string) {
	if rookVersion.LT(Rookv14) {
		selector := labels.SelectorFromSet(map[string]string{"app": "rook-ceph-operator"})
		return "rook-ceph-operator", selector.String()
	}
	selector := labels.SelectorFromSet(map[string]string{"app": "rook-ceph-tools"})
	return "rook-ceph-tools", selector.String()
}

func (c *Controller) countUniqueHostsWithOSD(ctx context.Context, rookVersion semver.Version) (int, error) {
	container, rookLabels := c.rookCephExecTarget(rookVersion)
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods(RookCephNS).List(ctx, opts)
	if err != nil {
		return 0, errors.Wrap(err, "list Rook pods")
	}
	if len(pods.Items) == 0 {
		return 0, errors.New("found no Rook pods for executing ceph commands")
	}

	cmd := []string{"ceph", "osd", "status"}
	exitCode, stdout, stderr, err := c.SyncExecutor.ExecContainer(ctx, RookCephNS, pods.Items[0].Name, container, cmd...)
	if err != nil {
		return 0, errors.Wrap(err, "exec ceph osd status")
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("exec `ceph osd status` exit code %d stderr: %s", exitCode, stderr)
	}

	hosts, err := parseCephOSDStatusHosts(stdout)
	if err != nil {
		log.Printf("Failed to parse `ceph osd status` stdout: %s", stdout)
		return 0, err
	}

	return len(hosts), nil
}

func parseCephOSDStatusHosts(s string) ([]string, error) {
	buf := bytes.NewBufferString(s)
	scanner := bufio.NewScanner(buf)

	hosts := map[string]bool{}

	for scanner.Scan() {
		text := strings.Replace(scanner.Text(), "|", " ", -1)
		matches := cephOSDStatusRX.FindStringSubmatch(text)
		if len(matches) < 2 {
			continue
		}
		hosts[matches[1]] = true
	}

	var ret []string
	for host := range hosts {
		ret = append(ret, host)
	}

	return ret, nil
}

func (c *Controller) PrioritizeRook(ctx context.Context) error {
	if err := c.prioritizeRookAgent(ctx); err != nil {
		return err
	}

	// cluster (rook-ceph) namespace resources
	selectors := []string{
		"app=rook-ceph-osd",
		"app=rook-ceph-mds",
		"app=rook-ceph-mgr",
		"app=rook-ceph-mon",
	}
	for _, selector := range selectors {
		c.Log.Debugf("Setting priority class for rook-ceph deployments with label %s", selector)
		if err := c.prioritizeRookDeployments(ctx, RookCephNS, selector); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) prioritizeRookAgent(ctx context.Context) error {
	dsClient := c.Config.Client.AppsV1().DaemonSets(RookCephNS)
	agentDS, err := dsClient.Get(ctx, "rook-ceph-agent", metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			c.Log.Debugf("rook-ceph-agent daemonset not found")
			return nil
		}
		return errors.Wrap(err, "get rook-ceph-agent daemonset")
	}
	if agentDS.Spec.Template.Spec.PriorityClassName != "" {
		c.Log.Debugf("rook-ceph-agent daemonset has priority class %s", agentDS.Spec.Template.Spec.PriorityClassName)
		return nil
	}

	c.Log.Infof("Setting rook-ceph-agent priorityclass %s", c.Config.RookPriorityClass)
	agentDS.Spec.Template.Spec.PriorityClassName = c.Config.RookPriorityClass
	_, err = dsClient.Update(ctx, agentDS, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrap(err, "update rook-ceph-agent priority")
	}
	return nil
}

func (c *Controller) prioritizeRookDeployments(ctx context.Context, namespace, selector string) error {
	opts := metav1.ListOptions{
		LabelSelector: selector,
	}
	deployments, err := c.Config.Client.AppsV1().Deployments(namespace).List(ctx, opts)
	if err != nil {
		return errors.Wrapf(err, "list deployments %q", selector)
	}
	for _, deployment := range deployments.Items {
		if deployment.Spec.Template.Spec.PriorityClassName != "" {
			c.Log.Debugf("Deployment %s has priority class %s", deployment.Name, deployment.Spec.Template.Spec.PriorityClassName)
			continue
		}
		deployment.Spec.Template.Spec.PriorityClassName = c.Config.RookPriorityClass
		c.Log.Infof("Setting %s priority class %s", deployment.Name, c.Config.RookPriorityClass)
		if _, err := c.Config.Client.AppsV1().Deployments(namespace).Update(ctx, &deployment, metav1.UpdateOptions{}); err != nil {
			return errors.Wrapf(err, "update %s priority class", deployment.Name)
		}
		// Change at most 1 deployment per reconcile to prevent disruptions
		break
	}

	return nil
}

func (c *Controller) GetCephCluster(ctx context.Context) (*cephv1.CephCluster, error) {
	return c.Config.CephV1.CephClusters(RookCephNS).Get(ctx, CephClusterName, metav1.GetOptions{})
}

// JSONPatchCephCluster patches the "rook-ceph" CephCluster with the given JSON
// patches.
func (c *Controller) JSONPatchCephCluster(ctx context.Context, patches []k8s.JSONPatchOperation) (*cephv1.CephCluster, error) {
	patchData, err := json.Marshal(patches)
	if err != nil {
		return nil, errors.Wrap(err, "marshal json patch")
	}
	c.Log.Debugf("Patching CephCluster %s with %s", CephClusterName, string(patchData))
	return c.Config.CephV1.CephClusters(RookCephNS).Patch(ctx, CephClusterName, apitypes.JSONPatchType, patchData, metav1.PatchOptions{})
}

func (c *Controller) cephOSDPoolSetSize(ctx context.Context, rookVersion semver.Version, cephVersion *semver.Version, name string, size int) error {
	args := []string{"ceph", "osd", "pool", "set", name, "size", strconv.Itoa(size)}
	if size == 1 && cephVersion != nil && cephVersion.Major >= CephPacific.Major {
		args = append(args, "--yes-i-really-mean-it")
	}
	return c.rookCephExec(ctx, rookVersion, args...)
}

func (c *Controller) cephOSDPoolSetMinSize(ctx context.Context, rookVersion semver.Version, name string, minSize int) error {
	args := []string{"ceph", "osd", "pool", "set", name, "min_size", strconv.Itoa(minSize)}
	return c.rookCephExec(ctx, rookVersion, args...)
}

// cephOSDPoolSetPgNumMin sets the pg_num_min property of the given pool to pgNumMin.
func (c *Controller) cephOSDPoolSetPgNumMin(ctx context.Context, rookVersion semver.Version, name string, pgNumMin uint64) error {
	args := []string{"ceph", "osd", "pool", "set", name, "pg_num_min", strconv.FormatUint(pgNumMin, 10)}
	return c.rookCephExec(ctx, rookVersion, args...)
}

// GetRookVersion gets the Rook version from the container image tag of the rook-ceph-operator
// deployment in the rook-ceph namespace.
func (c *Controller) GetRookVersion(ctx context.Context) (*semver.Version, error) {
	_, err := c.Config.Client.CoreV1().Namespaces().Get(ctx, RookCephNS, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "get rook-ceph namespace")
	}

	deploy, err := c.Config.Client.AppsV1().Deployments(RookCephNS).Get(ctx, "rook-ceph-operator", metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "get rook-ceph-operator deployment")
	}
	for _, container := range deploy.Spec.Template.Spec.Containers {
		if container.Name == "rook-ceph-operator" {
			parts := strings.Split(deploy.Spec.Template.Spec.Containers[0].Image, ":")
			imageTag := parts[len(parts)-1]
			rookVersion, err := semver.Parse(strings.TrimPrefix(imageTag, "v"))
			if err != nil {
				return nil, errors.Wrap(err, "parse semver")
			}
			return &rookVersion, nil
		}
	}
	return nil, errors.New("rook-ceph-operator container not found in deployment")
}

func (c *Controller) ensureCephClusterHelm(ctx context.Context, rookStorageClassName string) error {
	cephClusterChartArchive, _, err := charts.LatestChartByName("rook-ceph-cluster")
	if err != nil {
		return fmt.Errorf("unable to get rook-ceph-cluster chartfile: %w", err)
	}
	defer func(ebsfp fs.File) {
		err := ebsfp.Close()
		if err != nil {
			c.Log.Warnf("unable to close rook-ceph-cluster chartfile: %v", err)
		}
	}(cephClusterChartArchive)

	helmMgr, err := helm.NewHelmManager(ctx, "rook-ceph", c.Log)
	if err != nil {
		return fmt.Errorf("failed to initialize Helm Manager: %w", err)
	}

	chartValues, err := rookcephcluster.ValuesMap()
	if err != nil {
		return fmt.Errorf("failed to get rook-ceph chart values: %w", err)
	}

	if c.Config.RookCephImage != "" {
		if chartValues["cephClusterSpec"] == nil {
			chartValues["cephClusterSpec"] = make(map[string]interface{})
		}
		clusterSpec, ok := chartValues["cephClusterSpec"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("failed to get parse cephClusterSpec as map[string]interface{}")
		}

		if clusterSpec["cephVersion"] == nil {
			clusterSpec["cephVersion"] = make(map[string]interface{})
		}
		cephVersion, ok := clusterSpec["cephVersion"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("failed to get parse cephVersion as map[string]interface{}")
		}

		cephVersion["image"] = c.Config.RookCephImage
	}

	if err = helmMgr.InstallChartArchive(cephClusterChartArchive, chartValues, "", "rook-ceph"); err != nil {
		return fmt.Errorf("unable to apply chart: %w", err)
	}
	return nil
}

func (c *Controller) EnsureCephCluster(ctx context.Context, rookStorageClassName string) error {
	_, err := c.GetCephCluster(ctx)
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return errors.Wrap(err, "get ceph cluster")
		}
	}

	// create CephCluster
	if err = c.ensureCephClusterHelm(ctx, rookStorageClassName); err != nil {
		return err
	}

	// Create CephObjectStoreUser
	objectStoreUser := &cephv1.CephObjectStoreUser{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kurl",
			Namespace: "rook-ceph",
		},
		Spec: cephv1.ObjectStoreUserSpec{
			Store:       "rook-ceph-store",
			DisplayName: "kurl",
		},
	}
	if _, err := c.Config.CephV1.CephObjectStoreUsers(RookCephNS).Create(ctx, objectStoreUser, metav1.CreateOptions{}); err != nil {
		if util.IsAlreadyExists(err) {
			c.Log.Debugf("CephObjectStoreUser resource already exist")
		} else {
			return err
		}
	}
	return nil
}
