// Package ekcoops provides a Kubernetes Operator for an embedded kURL cluster.
// It automates the functions that would otherwise be required of a cluster administrator.
package ekcoops

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops/overrides"
	"github.com/replicatedhq/ekco/pkg/rook"
	"github.com/replicatedhq/ekco/pkg/util"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"go.uber.org/zap"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	minioMutex   = &sync.Mutex{}
	kotsadmMutex = &sync.Mutex{}
)

type Operator struct {
	config     Config
	client     kubernetes.Interface
	controller *cluster.Controller
	log        *zap.SugaredLogger
	mtx        sync.Mutex
}

func New(
	config Config,
	client kubernetes.Interface,
	controller *cluster.Controller,
	log *zap.SugaredLogger,
) *Operator {
	return &Operator{
		config:     config,
		client:     client,
		controller: controller,
		log:        log,
	}
}

func (o *Operator) Reconcile(ctx context.Context, nodes []corev1.Node, doFullReconcile bool) error {
	o.mtx.Lock()
	defer o.mtx.Unlock()

	if doFullReconcile {
		o.log.Debugf("Performing full reconcile")
	}

	var multiErr error

	var rookVersion *semver.Version
	rv, err := o.controller.GetRookVersion(ctx)
	if err != nil && !util.IsNotFoundErr(err) {
		o.log.Errorf("Failed to get Rook version: %v", err)
	} else if err == nil {
		rookVersion = rv
		o.log.Debugf("Rook version %s", rookVersion)
	}

	readyMasters, readyWorkers := util.NodeReadyCounts(nodes)
	for _, node := range nodes {
		err := o.reconcileNode(ctx, node, readyMasters, readyWorkers, rookVersion)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile node %s", node.Name))
		}
	}

	if rookVersion != nil {
		err := o.reconcileRook(ctx, *rookVersion, nodes, doFullReconcile)
		if err != nil {
			multiErr = multierror.Append(multiErr, err)
		}
	}

	if o.config.RotateCerts && doFullReconcile {
		err := o.RotateCerts(ctx, false)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate certs"))
		}
	}

	if o.config.EnableInternalLoadBalancer {
		if err := o.controller.ReconcileInternalLB(ctx, nodes); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "update internal loadbalancer"))
		}
	}

	if err := o.ReconcilePrometheus(ctx, len(nodes)); err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrap(err, "failed to reconcile prometheus"))
	}

	if err := o.controller.RestartFailedEnvoyPods(ctx); err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrap(err, "failed to reconcile failed envoy pod"))
	}

	if o.config.AutoApproveKubeletCertSigningRequests {
		if err := o.reconcileCertificateSigningRequests(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "reconcile csrs"))
		}
	}

	if o.config.EnableHAMinio {
		go func() {
			// run minio reconcile in the background as it can take ~unbounded time to migrate data
			if err := o.reconcileMinio(context.Background()); err != nil {
				o.log.Errorf("Failed to reconcile minio: %v", err)
			}
		}()
	}

	if o.config.EnableHAKotsadm {
		if err := o.reconcileKotsadm(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "reconcile kotsadm"))
		}
	}

	if o.config.RookMinimumNodeCount > 2 {
		if err := o.reconcileRookCluster(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "reconcile rook cluster"))
		}
	}

	return multiErr
}

func (o *Operator) reconcileNode(ctx context.Context, node corev1.Node, readyMasters, readyWorkers int, rookVersion *semver.Version) error {
	if o.config.PurgeDeadNodes && o.isDead(node) {
		if util.NodeIsMaster(node) && readyMasters < o.config.MinReadyMasterNodes {
			o.log.Debugf("Skipping auto-purge master: %d ready masters", readyMasters)
			return nil
		} else if readyWorkers < o.config.MinReadyWorkerNodes {
			o.log.Debugf("Skipping auto-purge worker: %d ready workers", readyWorkers)
			return nil
		}
		o.log.Infof("Automatically purging dead node %s", node.Name)
		err := o.controller.PurgeNode(ctx, node.Name, o.config.MaintainRookStorageNodes, rookVersion)
		if err != nil {
			return errors.Wrapf(err, "purge dead node %s", node.Name)
		}
	}

	if o.config.ClearDeadNodes && o.isDead(node) {
		err := o.controller.ClearNode(ctx, node.Name)
		if err != nil {
			return errors.Wrapf(err, "clear dead node %s", node.Name)
		}
	}

	return nil
}

func (o *Operator) reconcileRook(ctx context.Context, rookVersion semver.Version, nodes []corev1.Node, doFullReconcile bool) error {
	// if there is no CephCluster, we don't need to do anything
	_, err := o.controller.GetCephCluster(ctx)
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return errors.Wrapf(err, "get CephCluster")
		}
		return nil
	}

	var multiErr error

	if o.config.MaintainRookStorageNodes {
		shouldManageRookStorageNodesArray := false
		if o.config.RookStorageNodes != "" {
			// for now we do not care about the value of this flag, we just want to know if it is
			// set and we allow rook to manage storage nodes
			shouldManageRookStorageNodesArray = true
		}
		readyCount, err := o.ensureAllUsedForStorage(ctx, rookVersion, nodes, shouldManageRookStorageNodesArray)
		if err != nil {
			if !util.IsNotFoundErr(err) {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "ensure all ready nodes used for storage"))
			}
		} else {
			err := o.adjustPoolReplicationLevels(ctx, rookVersion, readyCount, doFullReconcile)
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "adjust pool replication levels"))
			}
			err = o.controller.ReconcileMonCount(ctx, readyCount)
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile mon count"))
			}
			err = o.controller.ReconcileMgrCount(ctx, rookVersion, readyCount)
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile mgr count"))
			}

		}
	}

	if o.config.ReconcileCephCSIResources {
		_, err := o.controller.SetCephCSIResources(ctx, rookVersion, len(nodes))
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "patch filesystem %s mds placement", o.config.CephFilesystem))
		}
	}

	if o.config.ReconcileRookMDSPlacement {
		err := o.controller.PatchFilesystemMDSPlacementMultinode(ctx, o.config.CephFilesystem, len(nodes))
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "patch filesystem %s mds placement", o.config.CephFilesystem))
		}
	}

	if o.config.RookPriorityClass != "" {
		if err := o.controller.PrioritizeRook(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "set rook priority class"))
		}
	}

	return multiErr
}

// a node is dead if unreachable for too long
func (o *Operator) isDead(node corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == util.UnreachableTaint && !taint.TimeAdded.IsZero() {
			return taint.TimeAdded.Time.Before(time.Now().Add(-o.config.NodeUnreachableToleration))
		}
	}
	return false
}

// ensureAlUsedForStorage will only append nodes. Removing nodes is only done during purge.
// Filter out not ready nodes since Rook fails to ever start an OSD on a node unless it's ready at
// the moment it detects the added node in the list.
func (o *Operator) ensureAllUsedForStorage(ctx context.Context, rookVersion semver.Version, nodes []corev1.Node, manageNodes bool) (int, error) {
	var names []string

	cluster, err := o.controller.GetCephCluster(ctx)
	if err != nil {
		return 0, errors.Wrapf(err, "get CephCluster config")
	}

	for _, node := range nodes {
		if shouldUseNodeForStorage(node, cluster, o.config.RookStorageNodesLabel, manageNodes) {
			names = append(names, node.Name)
		}
	}

	return o.controller.UseNodesForStorage(ctx, rookVersion, cluster, names, manageNodes)
}

// adjustPoolSizes changes ceph pool replication factors up and down
func (o *Operator) adjustPoolReplicationLevels(ctx context.Context, rookVersion semver.Version, numNodes int, doFullReconcile bool) error {
	factor := numNodes

	if factor < o.config.MinCephPoolReplication {
		factor = o.config.MinCephPoolReplication
	}
	if factor > o.config.MaxCephPoolReplication {
		factor = o.config.MaxCephPoolReplication
	}

	var multiErr error

	var cephVersion *semver.Version
	cephcluster, err := o.controller.GetCephCluster(ctx)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "get CephCluster config"))
	} else {
		ver, err := rook.GetCephVersion(*cephcluster)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "get ceph version"))
		} else {
			cephVersion = &ver
		}
	}

	didUpdate, err := o.controller.SetBlockPoolReplication(ctx, rookVersion, cephVersion, o.config.CephBlockPool, factor, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set pool %s replication to %d", o.config.CephBlockPool, factor))
	}

	ok, err := o.controller.SetFilesystemReplication(ctx, rookVersion, cephVersion, o.config.CephFilesystem, factor, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set filesystem %s replication to %d", o.config.CephFilesystem, factor))
	}
	didUpdate = didUpdate || ok

	ok, err = o.controller.SetObjectStoreReplication(ctx, rookVersion, cephVersion, o.config.CephObjectStore, factor, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set object store %s replication to %d", o.config.CephObjectStore, factor))
	}
	didUpdate = didUpdate || ok

	// There is no CR to compare the desired and current level.
	// Assume that if cephblockpool replication level has not yet been set then we need to do the same for device_health_metrics.
	_, err = o.controller.SetDeviceHealthMetricsReplication(ctx, rookVersion, cephVersion, o.config.CephBlockPool, factor, doFullReconcile || didUpdate)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set device_health_metrics replication to %d", factor))
	}

	return multiErr
}

func (o *Operator) RotateCerts(ctx context.Context, force bool) error {
	due, err := o.controller.CheckRotateCertsDue(ctx, force)
	if err != nil {
		return errors.Wrapf(err, "check if it's time to run cert rotation jobs")
	}
	if due {
		var multiErr error

		if err := o.controller.RotateContourCerts(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate contour certs"))
		}
		if err := o.controller.RotateRegistryCert(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate registry cert"))
		}
		if err := o.controller.RotateKurlProxyCert(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate kurl proxy cert"))
		}
		if err := o.controller.UpdateKubeletClientCertSecret(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "update kotsadm kubelet client cert secret"))
		}
		if err := o.controller.RotateAllCerts(ctx); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate certs on primaries"))
		}

		return multiErr
	} else {
		o.log.Debugf("Not yet time to rotate certs")
	}
	return nil
}

func shouldUseNodeForStorage(node corev1.Node, cluster *cephv1.CephCluster, rookStorageNodesLabel string, manageNodes bool) bool {
	if !util.NodeIsReady(node) {
		return false
	}
	if manageNodes {
		// If the list of storage nodes is provided, use the actual cluster nodes as the source of
		// truth
		for _, rookNode := range cluster.Spec.Storage.Nodes {
			if rookNode.Name == node.Name {
				return true
			}
		}
		return false
	}
	if rookStorageNodesLabel == "" {
		return true
	}
	for key, value := range node.Labels {
		if rookStorageNodesLabel == fmt.Sprintf("%s=%s", key, value) {
			return true
		}
	}
	return false
}

func (o *Operator) reconcileCertificateSigningRequests(ctx context.Context) error {
	csrList, err := o.client.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list csrs")
	}
	for _, csr := range csrList.Items {
		if csr.Spec.SignerName != "kubernetes.io/kubelet-serving" {
			continue
		}
		if len(csr.Status.Conditions) == 0 && len(csr.Status.Certificate) == 0 {
			csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
				Type:    certificatesv1.CertificateApproved,
				Reason:  "ekcoApprove",
				Message: "automated ekco approval of kubelet csr request",
				Status:  corev1.ConditionTrue,
			})
			_, err := o.client.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, &csr, metav1.UpdateOptions{})
			if err != nil {
				return errors.Wrapf(err, "approve csr %s", csr.Name)
			}
			o.log.Infof("CSR approval is successful %s", csr.Name)
		}
	}
	return nil
}

func (o *Operator) reconcileMinio(ctx context.Context) error {
	if overrides.MinIOPaused() {
		o.log.Debug("Not updating ha-minio as that has been paused")
		return nil // minio management is paused while other migrations are in progress
	}

	// only one operator should manage minio at a time, and if it's not us then we should not do anything
	if !minioMutex.TryLock() {
		return nil
	}
	defer minioMutex.Unlock()

	exists, err := o.controller.DoesHAMinioExist(ctx, o.config.MinioNamespace)
	if err != nil {
		return errors.Wrap(err, "determine if ha-minio exists to be managed")
	}
	if !exists {
		return nil // nothing to manage
	}

	nodes, err := o.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list nodes")
	}

	if m, w := util.NodeReadyCounts(nodes.Items); m+w >= 3 {
		err = o.controller.ScaleMinioStatefulset(ctx, o.config.MinioNamespace)
		if err != nil {
			return errors.Wrap(err, "scale minio statefulset")
		}

		// possibly migrate from old non-replicated minio and remove it
		_, err = o.client.AppsV1().Deployments(o.config.MinioNamespace).Get(ctx, "minio", metav1.GetOptions{})
		if err != nil {
			if !util.IsNotFoundErr(err) {
				return errors.Wrap(err, "get minio deployment")
			}
		} else {
			err = o.controller.MigrateMinioData(ctx, o.config.MinioUtilImage, o.config.MinioNamespace)
			if err != nil {
				return errors.Wrap(err, "migrate data to ha minio")
			}
		}
	}

	err = o.controller.MaybeRebalanceMinioServers(ctx, o.config.MinioNamespace)
	if err != nil {
		return errors.Wrap(err, "rebalance minio servers")
	}

	return nil
}

func (o *Operator) reconcileKotsadm(ctx context.Context) error {
	if overrides.KotsadmPaused() {
		o.log.Debug("Not updating kotsadm as that has been paused")
		return nil // kotsadm management is paused while other migrations are in progress
	}
	nodes, err := o.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list nodes")
	}

	if m, w := util.NodeReadyCounts(nodes.Items); m+w >= 3 {
		err := o.controller.EnableHAKotsadm(ctx, metav1.NamespaceDefault)
		if err != nil {
			return errors.Wrap(err, "enable HA kotsadm")
		}
	}

	return nil
}

func (o *Operator) reconcileRookCluster(ctx context.Context) error {
	nodes, err := o.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "list nodes")
	}

	if m, w := util.NodeReadyCounts(nodes.Items); m+w >= o.config.RookMinimumNodeCount {
		o.log.Debugf("reconcileRookCluster(): Rook minimum node count of %d has been met by this cluster.\n", o.config.RookMinimumNodeCount)
		err := o.controller.EnsureCephCluster(ctx, o.config.RookStorageClass)
		if err != nil {
			return errors.Wrap(err, "ensure ceph cluster")
		}
	}
	return nil
}
