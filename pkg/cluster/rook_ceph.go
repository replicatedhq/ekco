package cluster

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/k8s"
	"github.com/replicatedhq/ekco/pkg/util"
	rookv1 "github.com/rook/rook/pkg/apis/rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var cephErrENOENT = errors.New("Ceph ENOENT")
var cephOSDStatusRX = regexp.MustCompile(`^\s*\d\s+(?P<host>\S+)`)

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
			cluster.Spec.Storage.Nodes = append(cluster.Spec.Storage.Nodes, rookv1.Node{
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

	return c.countUniqueHostsWithOSD()
}

func (c *Controller) removeCephClusterStorageNode(name string) error {
	cluster, err := c.Config.CephV1.CephClusters(RookCephNS).Get(CephClusterName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}
	var keep []rookv1.Node
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
	if current < level || doFullReconcile {
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
		if c.Config.RookVersion.LT(Rookv14) {
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
			err = c.rookCephExec("ceph", "osd", "pool", "set", name, "size", strconv.Itoa(level))
			if err != nil {
				return errors.Wrapf(err, "set block pool %q size to %d", name, minSize)
			}
			err = c.rookCephExec("ceph", "osd", "pool", "set", name, "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrapf(err, "set block pool %q min_size to %d", name, minSize)
			}
		}
	}

	return nil
}

func (c *Controller) SetDeviceHealthMetricsReplication(level int, doFullReconcile bool) error {
	if c.Config.RookVersion.LT(Rookv14) {
		return nil
	}
	// There's no CR to compare the desired and current level
	if !doFullReconcile {
		return nil
	}

	minSize := 1
	if level > 1 {
		minSize = 2
	}

	c.Log.Debugf("Ensuring %s replication level is %d", CephDeviceHealthMetricsPool, level)

	err := c.rookCephExec("ceph", "osd", "pool", "set", CephDeviceHealthMetricsPool, "size", strconv.Itoa(level))
	if err != nil {
		return errors.Wrapf(err, "scale %s pool size", CephDeviceHealthMetricsPool)
	}
	err = c.rookCephExec("ceph", "osd", "pool", "set", CephDeviceHealthMetricsPool, "min_size", strconv.Itoa(minSize))
	if err != nil {
		return errors.Wrapf(err, "scale %s pool min_size", CephDeviceHealthMetricsPool)
	}

	return nil
}

func (c *Controller) ReconcileMonCount(count int) error {
	// single mon for 1 or 2 node cluster, 3 mons for all other clusters
	if count < 3 {
		count = 1
	} else {
		count = 3
	}

	cluster, err := c.Config.CephV1.CephClusters(RookCephNS).Get(CephClusterName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get CephCluster config")
	}

	if cluster.Spec.Mon.Count == count {
		return nil
	}
	if cluster.Spec.Mon.Count > count {
		c.Log.Debugf("Will not reduce mon count from %s to %s", cluster.Spec.Mon.Count, count)
		return nil
	}

	c.Log.Infof("Changing mon count from %d to %d", cluster.Spec.Mon.Count, count)
	cluster.Spec.Mon.Count = count
	_, err = c.Config.CephV1.CephClusters("rook-ceph").Update(cluster)
	if err != nil {
		return errors.Wrap(err, "update CephCluster with new mon count")
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
		if current < level {
			fs.Spec.DataPools[i].Replicated.Size = uint(level)
			changed = true
		}
	}
	current := int(fs.Spec.MetadataPool.Replicated.Size)
	if current < level {
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
		if c.Config.RookVersion.LT(Rookv14) {
			minSize := 1
			if level > 1 {
				minSize = 2
			}
			// Changing the size in the CephFilesystem has no effect so it needs to be set manually
			// https://github.com/rook/rook/issues/3144
			err = c.rookCephExec("ceph", "osd", "pool", "set", RookCephSharedFSMetadataPool, "size", strconv.Itoa(level))
			if err != nil {
				return errors.Wrapf(err, "scale shared fs metadata pool size")
			}
			err = c.rookCephExec("ceph", "osd", "pool", "set", RookCephSharedFSMetadataPool, "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrapf(err, "scale shared fs metadata pool min_size")
			}
			err = c.rookCephExec("ceph", "osd", "pool", "set", RookCephSharedFSDataPool, "size", strconv.Itoa(level))
			if err != nil {
				return errors.Wrapf(err, "scale shared fs data pool size")
			}
			err = c.rookCephExec("ceph", "osd", "pool", "set", RookCephSharedFSDataPool, "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrapf(err, "scale shared fs data pool min_size")
			}
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
	if current < level {
		os.Spec.DataPool.Replicated.Size = uint(level)
		changed = true
	}

	current = int(os.Spec.MetadataPool.Replicated.Size)
	if current < level {
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
		// Changing the size in the CephObjectStore has no effect in Rook 1.0 so it needs to be set
		// manually https://github.com/rook/rook/issues/4341
		if c.Config.RookVersion.LT(Rookv14) {
			err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, RookCephObjectStoreRootPool), "size", strconv.Itoa(level))
			if err != nil {
				return errors.Wrap(err, "scale ceph object store root pool size")
			}
			err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, RookCephObjectStoreRootPool), "min_size", strconv.Itoa(minSize))
			if err != nil {
				return errors.Wrap(err, "scale ceph object store root pool min_size")
			}
			for _, pool := range RookCephObjectStoreMetadataPools {
				err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "size", strconv.Itoa(level))
				if err == cephErrENOENT {
					// the non-ec metadata pool doesn't always exist
					continue
				}
				if err != nil {
					return errors.Wrapf(err, "scale ceph object store metadata pool %s size", pool)
				}
				err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "min_size", strconv.Itoa(minSize))
				if err != nil {
					return errors.Wrapf(err, "scale ceph object store metadata pool %s min_size", pool)
				}
			}
			for _, pool := range RookCephObjectStoreDataPools {
				err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "size", strconv.Itoa(level))
				if err != nil {
					return errors.Wrapf(err, "scale ceph object store data pool %s size", pool)
				}
				err = c.rookCephExec("ceph", "osd", "pool", "set", objectStorePoolName(name, pool), "min_size", strconv.Itoa(minSize))
				if err != nil {
					return errors.Wrapf(err, "scale ceph object store data pool %s min_size", pool)
				}
			}
		}
	}

	return nil
}

// Toolbox is deployed with Rook 1.4 since ceph commands can't be executed in operator
func (c *Controller) rookCephExec(cmd ...string) error {
	container, rookLabels := c.rookCephExecTarget()
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return errors.Wrap(err, "list Rook pods")
	}
	if len(pods.Items) == 0 {
		return errors.Wrap(err, "found no Rook pods for executing ceph commands")
	}

	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, cmd...)
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

func (c *Controller) execCephOSDPurge(osdID string, hostname string) error {
	container, rookLabels := c.rookCephExecTarget()
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return errors.Wrap(err, "list Rook tools pods")
	}
	if len(pods.Items) == 0 {
		return errors.Wrapf(err, "found no Rook pods for executing ceph commands")
	}
	// ignore error - OSD is probably already down
	k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, "ceph", "osd", "down", osdID)

	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, "ceph", "osd", "purge", osdID, "--yes-i-really-mean-it")
	if exitCode != 0 {
		c.Log.Debugf("`ceph osd purge %s` stdout: %s", osdID, stdout)
		return fmt.Errorf("Failed to purge OSD: %s", stderr)
	}
	if err != nil {
		return errors.Wrap(err, "ceph osd purge")
	}

	// This removes the phantom OSD from the output of `ceph osd tree`
	exitCode, stdout, stderr, err = k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, "ceph", "osd", "crush", "rm", hostname)
	if exitCode != 0 {
		c.Log.Debugf("`ceph osd crush rm %s` stdout: %s", hostname, stdout)
		return fmt.Errorf("Failed to rm %s from crush map: %s", hostname, stderr)
	}
	if err != nil {
		return errors.Wrap(err, "ceph osd purge")
	}

	return nil
}

func (c *Controller) CephFilesystemOK(name string) (bool, error) {
	container, rookLabels := c.rookCephExecTarget()
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
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
	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, "ceph", "mds", "metadata")
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

// returns labelSelector, containerName
func (c *Controller) rookCephExecTarget() (string, string) {
	if c.Config.RookVersion.LT(Rookv14) {
		selector := labels.SelectorFromSet(map[string]string{"app": "rook-ceph-operator"})
		return "rook-ceph-operator", selector.String()
	}
	selector := labels.SelectorFromSet(map[string]string{"app": "rook-ceph-tools"})
	return "rook-ceph-tools", selector.String()
}

func (c *Controller) countUniqueHostsWithOSD() (int, error) {
	container, rookLabels := c.rookCephExecTarget()
	opts := metav1.ListOptions{
		LabelSelector: rookLabels,
	}
	pods, err := c.Config.Client.CoreV1().Pods("rook-ceph").List(opts)
	if err != nil {
		return 0, errors.Wrap(err, "list Rook pods")
	}
	if len(pods.Items) == 0 {
		return 0, errors.New("found no Rook pods for executing ceph commands")
	}

	cmd := []string{"ceph", "osd", "status"}
	exitCode, stdout, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, "rook-ceph", pods.Items[0].Name, container, cmd...)
	if err != nil {
		return 0, errors.Wrap(err, "exec ceph osd status")
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("Exec `ceph osd status` exit code %d stderr: %s", exitCode, stderr)
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

func (c *Controller) PrioritizeRook() error {
	if err := c.prioritizeRookAgent(); err != nil {
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
		if err := c.prioritizeRookDeployments("rook-ceph", selector); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) prioritizeRookAgent() error {
	dsClient := c.Config.Client.AppsV1().DaemonSets("rook-ceph")
	agentDS, err := dsClient.Get("rook-ceph-agent", metav1.GetOptions{})
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
	_, err = dsClient.Update(agentDS)
	if err != nil {
		return errors.Wrap(err, "update rook-ceph-agent priority")
	}
	return nil
}

func (c *Controller) prioritizeRookDeployments(namespace, selector string) error {
	opts := metav1.ListOptions{
		LabelSelector: selector,
	}
	deployments, err := c.Config.Client.AppsV1().Deployments(namespace).List(opts)
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
		if _, err := c.Config.Client.AppsV1().Deployments(namespace).Update(&deployment); err != nil {
			return errors.Wrapf(err, "update %s priority class", deployment.Name)
		}
		// Change at most 1 deployment per reconcile to prevent disruptions
		break
	}

	return nil
}
