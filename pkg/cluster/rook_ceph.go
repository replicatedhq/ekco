package cluster

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/k8s"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/rook/rook/pkg/apis/rook.io/v1alpha2"
	rookv1alpha2 "github.com/rook/rook/pkg/apis/rook.io/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var cephErrENOENT = errors.New("Ceph ENOENT")

// returns the number of nodes used for storage, which may be higher than the number of names passed in
// if a node is currenlty not ready but has not been purged
func (c *Controller) UseNodesForStorage(names []string) (int, error) {
	cluster, err := c.Config.CephV1.CephClusters(RookCephNS).Get(CephClusterName, metav1.GetOptions{})
	if err != nil {
		return 0, errors.Wrapf(err, "get CephCluster config")
	}
	storageNodes := make(map[string]bool, len(cluster.Spec.Storage.Nodes))
	for _, storageNode := range cluster.Spec.Storage.Nodes {
		storageNodes[storageNode.Name] = true
	}
	changed := false
	for _, name := range names {
		if !storageNodes[name] {
			c.Log.Infof("Adding node %q to CephCluster node storage list", name)
			cluster.Spec.Storage.Nodes = append(cluster.Spec.Storage.Nodes, rookv1alpha2.Node{
				Name: name,
			})
			changed = true
		}
	}
	if changed {
		cluster.Spec.Storage.UseAllNodes = false
		_, err := c.Config.CephV1.CephClusters("rook-ceph").Update(cluster)
		if err != nil {
			return 0, errors.Wrap(err, "update CephCluster with new storage node list")
		}
	}

	return len(cluster.Spec.Storage.Nodes), nil
}

func (c *Controller) removeCephClusterStorageNode(name string) error {
	cluster, err := c.Config.CephV1.CephClusters(RookCephNS).Get(CephClusterName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}
	var keep []v1alpha2.Node
	for _, node := range cluster.Spec.Storage.Nodes {
		if node.Name == name {
			c.Log.Infof("Removing node %q from CephCluster storage list", name)
		} else {
			keep = append(keep, node)
		}
	}
	if !reflect.DeepEqual(keep, cluster.Spec.Storage.Nodes) {
		cluster.Spec.Storage.Nodes = keep
		_, err = c.Config.CephV1.CephClusters("rook-ceph").Update(cluster)
		if err != nil {
			return errors.Wrap(err, "update CephCluster with new storage node list")
		}
		c.Log.Infof("Purge node %q: removed from CephCluster node storage list", name)
	}

	return nil
}

// returns the osd id if found
func (c *Controller) deleteK8sDeploymentOSD(name string) (string, error) {
	var osdID string

	rookOSDLabels := map[string]string{"app": "rook-ceph-osd"}
	opts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(rookOSDLabels).String(),
	}
	deploys, err := c.Config.Client.AppsV1().Deployments("rook-ceph").List(opts)
	if err != nil {
		return "", errors.Wrap(err, "list Rook OSD deployments")
	}
	for _, deploy := range deploys.Items {
		hostname := deploy.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]
		if hostname == name {
			labels := deploy.ObjectMeta.GetLabels()
			osdID = labels["ceph-osd-id"]
			background := metav1.DeletePropagationBackground
			opts := &metav1.DeleteOptions{
				PropagationPolicy: &background,
			}
			err := c.Config.Client.AppsV1().Deployments("rook-ceph").Delete(deploy.Name, opts)
			if err != nil {
				return "", errors.Wrapf(err, "delete deployment %s", deploy.Name)
			}
			c.Log.Infof("Deleted OSD Deployment for node %s", name)
			break
		}
	}

	return osdID, nil
}

// SetBlockPoolReplicationLevel ignores NotFound errors.
func (c *Controller) SetBlockPoolReplication(name string, level int, doFullReconcile bool) error {
	if name == "" {
		return nil
	}

	pool, err := c.Config.CephV1.CephBlockPools(RookCephNS).Get(name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil
		}
		return errors.Wrapf(err, "get CephBlockPool %s", name)
	}
	current := int(pool.Spec.Replicated.Size)
	if current != level || doFullReconcile {
		if current != level {
			c.Log.Infof("Changing CephBlockPool replication level from %d to %d", current, level)
		} else {
			c.Log.Debugf("Ensuring CephBlockPool replication level is %d", level)
		}
		pool.Spec.Replicated.Size = uint(level)
		_, err := c.Config.CephV1.CephBlockPools(RookCephNS).Update(pool)
		if err != nil {
			return errors.Wrapf(err, "update CephBlockPool %s", name)
		}
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
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", name, "size", strconv.Itoa(level))
		if err != nil {
			return errors.Wrapf(err, "set block pool %q size to %d", name, minSize)
		}
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", name, "min_size", strconv.Itoa(minSize))
		if err != nil {
			return errors.Wrapf(err, "set block pool %q min_size to %d", name, minSize)
		}
	}

	return nil
}

// SetSharedFilesystemReplication ignores NotFound errors.
func (c *Controller) SetFilesystemReplication(name string, level int, doFullReconcile bool) error {
	if name == "" {
		return nil
	}

	fs, err := c.Config.CephV1.CephFilesystems(RookCephNS).Get(name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil
		}
		return errors.Wrapf(err, "get Filesystem %s", name)
	}
	changed := false
	for i, pool := range fs.Spec.DataPools {
		current := int(pool.Replicated.Size)
		if current != level {
			fs.Spec.DataPools[i].Replicated.Size = uint(level)
			changed = true
		}
	}
	current := int(fs.Spec.MetadataPool.Replicated.Size)
	if current != level {
		fs.Spec.MetadataPool.Replicated.Size = uint(level)
		changed = true
	}

	if changed || doFullReconcile {
		if changed {
			c.Log.Infof("Changing CephFilesystem pool replication level from %d to %d", current, level)
		} else {
			c.Log.Debugf("Ensuring CephFilesystem pool replication level is %d", level)
		}
		_, err := c.Config.CephV1.CephFilesystems("rook-ceph").Update(fs)
		if err != nil {
			return errors.Wrapf(err, "update Filesystem %s", name)
		}
		minSize := 1
		if level > 1 {
			minSize = 2
		}
		// Changing the size in the CephFilesystem has no effect so it needs to be set manually
		// https://github.com/rook/rook/issues/3144
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", RookCephSharedFSMetadataPool, "size", strconv.Itoa(level))
		if err != nil {
			return errors.Wrapf(err, "scale shared fs metadata pool size")
		}
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", RookCephSharedFSMetadataPool, "min_size", strconv.Itoa(minSize))
		if err != nil {
			return errors.Wrapf(err, "scale shared fs metadata pool min_size")
		}
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", RookCephSharedFSDataPool, "size", strconv.Itoa(level))
		if err != nil {
			return errors.Wrapf(err, "scale shared fs data pool size")
		}
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", RookCephSharedFSDataPool, "min_size", strconv.Itoa(minSize))
		if err != nil {
			return errors.Wrapf(err, "scale shared fs data pool min_size")
		}
	}

	return nil
}

// SetObjectStoreReplication ignores NotFound errors.
func (c *Controller) SetObjectStoreReplication(name string, level int, doFullReconcile bool) error {
	if name == "" {
		return nil
	}

	os, err := c.Config.CephV1.CephObjectStores(RookCephNS).Get(name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil
		}
		return errors.Wrapf(err, "get CephObjectStore %s", name)
	}
	changed := false

	current := int(os.Spec.DataPool.Replicated.Size)
	if current != level {
		os.Spec.DataPool.Replicated.Size = uint(level)
		changed = true
	}

	current = int(os.Spec.MetadataPool.Replicated.Size)
	if current != level {
		os.Spec.MetadataPool.Replicated.Size = uint(level)
		changed = true
	}

	if changed || doFullReconcile {
		minSize := 1
		if level > 1 {
			minSize = 2
		}
		if changed {
			c.Log.Infof("Changing CephObjectStore pool replication level from %d to %d", current, level)
		} else {
			c.Log.Debugf("Ensuring CephOjbectStore pool replication level is %d", level)
		}
		_, err := c.Config.CephV1.CephObjectStores(RookCephNS).Update(os)
		if err != nil {
			return errors.Wrapf(err, "update CephObjectStore %s", name)
		}
		// Changing the size in the CephObjectStore has no effect so it needs to be set manually
		// https://github.com/rook/rook/issues/4341
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, RookCephObjectStoreRootPool), "size", strconv.Itoa(level))
		if err != nil {
			return errors.Wrap(err, "scale ceph object store root pool size")
		}
		err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, RookCephObjectStoreRootPool), "min_size", strconv.Itoa(minSize))
		if err != nil {
			return errors.Wrap(err, "scale ceph object store root pool min_size")
		}
		for _, pool := range RookCephObjectStoreMetadataPools {
			err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "size", strconv.Itoa(level))
			if err == cephErrENOENT {
				// the non-ec metadata pool doesn't always exist
				continue
			}
			if err != nil {
				return errors.Wrapf(err, "scale ceph object store metadata pool %s size", pool)
			}
			err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrapf(err, "scale ceph object store metadata pool %s min_size", pool)
			}
		}
		for _, pool := range RookCephObjectStoreDataPools {
			err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "size", strconv.Itoa(level))
			if err != nil {
				return errors.Wrapf(err, "scale ceph object store data pool %s size", pool)
			}
			err = c.rookCephToolsExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrapf(err, "scale ceph object store data pool %s min_size", pool)
			}
		}
	}

	return nil
}

func (c *Controller) rookCephToolsExec(cmd ...string) error {
	rookToolsLabels := map[string]string{"app": "rook-ceph-tools"}
	opts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(rookToolsLabels).String(),
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) != 1 {
		return errors.Wrapf(err, "found %d Rook tools pods", len(pods.Items))
	}

	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, "rook-ceph-tools", cmd...)
	if err != nil {
		return err
	}
	if exitCode == 2 {
		c.Log.Debugf("Rook ceph tools exec %q exited with code %d and stderr: %s", cmd, exitCode, stderr)
		return cephErrENOENT
	}
	if exitCode != 0 {
		c.Log.Infof("Rook ceph tools exec %q exited with code %d and stderr: %s", cmd, exitCode, stderr)

		return fmt.Errorf("exec %q: %d", cmd, exitCode)
	}

	c.Log.Debugf("Exec Rook ceph tools %q exited with code %d and stdout: %s", cmd, exitCode, stdout)

	return nil
}

func (c *Controller) execCephOSDPurge(osdID string) error {
	rookToolsLabels := map[string]string{"app": "rook-ceph-tools"}
	opts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(rookToolsLabels).String(),
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) != 1 {
		return errors.Wrapf(err, "found %d Rook tools pods", len(pods.Items))
	}
	// ignore error
	k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, "rook-ceph-tools", "ceph", "osd", "down", osdID)
	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, "rook-ceph-tools", "ceph", "osd", "purge", osdID, "--yes-i-really-mean-it")
	if exitCode != 0 {
		c.Log.Debugf("`ceph osd purge %s` stdout: %s", osdID, stdout)
		return fmt.Errorf("Failed to purge OSD: %s", stderr)
	}

	return nil
}

func (c *Controller) CephFilesystemOK(name string) (bool, error) {
	rookToolsLabels := map[string]string{"app": "rook-ceph-tools"}
	opts := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(rookToolsLabels).String(),
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return false, errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) != 1 {
		return false, errors.Wrapf(err, "found %d Rook tools pods", len(pods.Items))
	}
	// The filesystem will appear in `ceph fs ls` before it's ready to use. `ceph mds metadata` is
	// better because it waits for the mds daemons to be running
	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, "rook-ceph-tools", "ceph", "mds", "metadata")
	if exitCode != 0 {
		c.Log.Debugf("`ceph fs ls` stdout: %s", stdout)
		return false, fmt.Errorf("Failed to list ceph filesystems: %s", stderr)
	}

	mdsA := fmt.Sprintf("%s-a", name)
	mdsB := fmt.Sprintf("%s-b", name)

	return strings.Contains(stdout, mdsA) && strings.Contains(stdout, mdsB), nil
}

func (c *Controller) WaitCephFilesystem(ctx context.Context, name string) error {
	ok, err := c.CephFilesystemOK(name)
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
			ok, err := c.CephFilesystemOK(name)
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
