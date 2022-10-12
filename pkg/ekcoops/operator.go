// Package ekcoops provides a Kubernetes Operator for an embedded kURL cluster.
// It automates the functions that would otherwise be required of a cluster administrator.
package ekcoops

import (
	"context"
	"fmt"
	"sync"
	"time"

	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/util"
	"go.uber.org/zap"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

func (o *Operator) Reconcile(nodes []corev1.Node, doFullReconcile bool) error {
	o.mtx.Lock()
	defer o.mtx.Unlock()

	if doFullReconcile {
		o.log.Debugf("Performing full reconcile")
	}

	var multiErr error

	readyMasters, readyWorkers := util.NodeReadyCounts(nodes)
	for _, node := range nodes {
		err := o.reconcileNode(node, readyMasters, readyWorkers)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile node %s", node.Name))
		}
	}

	err := o.reconcileRook(nodes, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, err)
	}

	if o.config.RotateCerts && doFullReconcile {
		err := o.RotateCerts(false)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate certs"))
		}
	}

	if o.config.EnableInternalLoadBalancer {
		if err := o.controller.ReconcileInternalLB(context.Background(), nodes); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "update internal loadbalancer"))
		}
	}

	if err := o.ReconcilePrometheus(len(nodes)); err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrap(err, "failed to reconcile prometheus"))
	}

	if err := o.controller.RestartFailedEnvoyPods(context.Background()); err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrap(err, "failed to reconcile failed envoy pod"))
	}

	if o.config.AutoApproveKubeletCertSigningRequests {
		if err := o.reconcileCertificateSigningRequests(); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "reconcile csrs"))
		}
	}

	return multiErr
}

func (o *Operator) reconcileNode(node corev1.Node, readyMasters, readyWorkers int) error {
	if o.config.PurgeDeadNodes && o.isDead(node) {
		if util.NodeIsMaster(node) && readyMasters < o.config.MinReadyMasterNodes {
			o.log.Debugf("Skipping auto-purge master: %d ready masters", readyMasters)
			return nil
		} else if readyWorkers < o.config.MinReadyWorkerNodes {
			o.log.Debugf("Skipping auto-purge worker: %d ready workers", readyWorkers)
			return nil
		}
		o.log.Infof("Automatically purging dead node %s", node.Name)
		err := o.controller.PurgeNode(context.TODO(), node.Name, o.config.MaintainRookStorageNodes)
		if err != nil {
			return errors.Wrapf(err, "purge dead node %s", node.Name)
		}
	}

	if o.config.ClearDeadNodes && o.isDead(node) {
		err := o.controller.ClearNode(context.TODO(), node.Name)
		if err != nil {
			return errors.Wrapf(err, "clear dead node %s", node.Name)
		}
	}

	return nil
}

func (o *Operator) reconcileRook(nodes []corev1.Node, doFullReconcile bool) error {
	var multiErr error

	if o.config.MaintainRookStorageNodes {
		readyCount, err := o.ensureAllUsedForStorage(nodes)
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "ensure all ready nodes used for storage"))
		} else {
			err := o.adjustPoolReplicationLevels(readyCount, doFullReconcile)
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "adjust pool replication levels"))
			} else {
				err := o.controller.ReconcileMonCount(context.TODO(), readyCount)
				if err != nil {
					multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile mon count"))
				}
				err = o.controller.ReconcileMgrCount(context.TODO(), readyCount)
				if err != nil {
					multiErr = multierror.Append(multiErr, errors.Wrapf(err, "reconcile mgr count"))
				}
			}
		}
	}

	if o.config.ReconcileCephCSIResources {
		_, err := o.controller.SetCephCSIResources(context.TODO(), len(nodes))
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "patch filesystem %s mds placement", o.config.CephFilesystem))
		}
	}

	if o.config.ReconcileRookMDSPlacement {
		err := o.controller.PatchFilesystemMDSPlacementMultinode(o.config.CephFilesystem, len(nodes))
		if err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrapf(err, "patch filesystem %s mds placement", o.config.CephFilesystem))
		}
	}

	if o.config.RookPriorityClass != "" {
		if err := o.controller.PrioritizeRook(); err != nil {
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
func (o *Operator) ensureAllUsedForStorage(nodes []corev1.Node) (int, error) {
	var names []string

	for _, node := range nodes {
		if shouldUseNodeForStorage(node, o.config.RookStorageNodesLabel) {
			names = append(names, node.Name)
		}
	}

	return o.controller.UseNodesForStorage(names)
}

// adjustPoolSizes changes ceph pool replication factors up and down
func (o *Operator) adjustPoolReplicationLevels(numNodes int, doFullReconcile bool) error {
	factor := numNodes

	if factor < o.config.MinCephPoolReplication {
		factor = o.config.MinCephPoolReplication
	}
	if factor > o.config.MaxCephPoolReplication {
		factor = o.config.MaxCephPoolReplication
	}

	var multiErr error

	cephcluster, err := o.controller.GetCephCluster(context.TODO())
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "get CephCluster config"))
	}

	didUpdate, err := o.controller.SetBlockPoolReplication(o.config.CephBlockPool, factor, cephcluster, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set pool %s replication to %d", o.config.CephBlockPool, factor))
	}

	ok, err := o.controller.SetFilesystemReplication(o.config.CephFilesystem, factor, cephcluster, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set filesystem %s replication to %d", o.config.CephFilesystem, factor))
	}
	didUpdate = didUpdate || ok

	ok, err = o.controller.SetObjectStoreReplication(o.config.CephObjectStore, factor, cephcluster, doFullReconcile)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set object store %s replication to %d", o.config.CephObjectStore, factor))
	}
	didUpdate = didUpdate || ok

	// There is no CR to compare the desired and current level.
	// Assume that if cephblockpool replication level has not yet been set then we need to do the same for device_health_metrics.
	_, err = o.controller.SetDeviceHealthMetricsReplication(o.config.CephBlockPool, factor, cephcluster, doFullReconcile || didUpdate)
	if err != nil {
		multiErr = multierror.Append(multiErr, errors.Wrapf(err, "set health_device_metrics replication to %d", factor))
	}

	return multiErr
}

func (o *Operator) RotateCerts(force bool) error {
	due, err := o.controller.CheckRotateCertsDue(force)
	if err != nil {
		return errors.Wrapf(err, "check if it's time to run cert rotation jobs")
	}
	if due {
		var multiErr error

		if err := o.controller.RotateContourCerts(); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate contour certs"))
		}
		if err := o.controller.RotateRegistryCert(); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate registry cert"))
		}
		if err := o.controller.RotateKurlProxyCert(); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate kurl proxy cert"))
		}
		if err := o.controller.UpdateKubeletClientCertSecret(); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "update kotsadm kubelet client cert secret"))
		}
		if err := o.controller.RotateAllCerts(context.Background()); err != nil {
			multiErr = multierror.Append(multiErr, errors.Wrap(err, "rotate certs on primaries"))
		}

		return multiErr
	} else {
		o.log.Debugf("Not yet time to rotate certs")
	}
	return nil
}

func shouldUseNodeForStorage(node corev1.Node, rookStorageNodesLabel string) bool {
	if !util.NodeIsReady(node) {
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

func (o *Operator) reconcileCertificateSigningRequests() error {
	csrList, err := o.client.CertificatesV1().CertificateSigningRequests().List(context.TODO(), metav1.ListOptions{})
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
			_, err := o.client.CertificatesV1().CertificateSigningRequests().UpdateApproval(context.TODO(), csr.Name, &csr, metav1.UpdateOptions{})
			if err != nil {
				return errors.Wrapf(err, "approve csr %s", csr.Name)
			}
			o.log.Infof("CSR approval is successful %s", csr.Name)
		}
	}
	return nil
}
