// Package ekcoops provides a Kubernetes Operator for a Replicated Embedded Kubernetes cluster.
// It automates the functions that would otherwise be required of a cluster administrator.
package ekcoops

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type Operator struct {
	config     Config
	client     kubernetes.Interface
	controller *cluster.Controller
	log        *logger.Logger
	mtx        sync.Mutex
}

func New(
	config Config,
	client kubernetes.Interface,
	controller *cluster.Controller,
	log *logger.Logger,
) *Operator {
	return &Operator{
		config:     config,
		client:     client,
		controller: controller,
		log:        log,
	}
}

func (o *Operator) Reconcile(nodes []corev1.Node) error {
	o.mtx.Lock()
	defer o.mtx.Unlock()

	readyMasters, readyWorkers := util.NodeReadyCounts(nodes)
	for _, node := range nodes {
		err := o.reconcile(node, readyMasters, readyWorkers)
		if err != nil {
			return errors.Wrapf(err, "reconcile node %s", node.Name)
		}
	}

	if o.config.MaintainRookStorageNodes {
		readyCount, err := o.ensureAllUsedForStorage(nodes)
		if err != nil {
			return errors.Wrapf(err, "ensure all ready nodes used for storage")
		}
		err = o.adjustPoolReplicationLevels(readyCount)
		if err != nil {
			return errors.Wrapf(err, "adjust pool replication levels")
		}
	}

	return nil
}

func (o *Operator) reconcile(node corev1.Node, readyMasters, readyWorkers int) error {
	if o.config.PurgeDeadNodes && o.isDead(node) {
		if util.NodeIsMaster(node) && readyMasters < o.config.MinReadyMasterNodes {
			o.log.Debug("Skipping auto-purge master: %d ready masters", readyMasters)
			return nil
		} else if readyWorkers < o.config.MinReadyWorkerNodes {
			o.log.Debug("Skipping auto-purge worker: %d ready workers", readyWorkers)
			return nil
		}
		o.log.Info("Ekco automatically purging dead node %s", node.Name)
		err := o.controller.PurgeNode(context.TODO(), node.Name, o.config.MaintainRookStorageNodes)
		if err != nil {
			return errors.Wrapf(err, "purge dead node %s", node.Name)
		}
	}

	return nil
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
		if util.NodeIsReady(node) {
			names = append(names, node.Name)
		}
	}

	return o.controller.UseNodesForStorage(names)
}

// adjustPoolSizes changes ceph pool replication factors up and down
func (o *Operator) adjustPoolReplicationLevels(numNodes int) error {
	factor := numNodes

	if factor < o.config.MinCephPoolReplication {
		factor = o.config.MinCephPoolReplication
	}
	if factor > o.config.MaxCephPoolReplication {
		factor = o.config.MaxCephPoolReplication
	}

	err := o.controller.SetPoolReplication(o.config.CephBlockPool, factor)
	if err != nil {
		return errors.Wrapf(err, "set pool %s replication to %d", o.config.CephBlockPool, factor)
	}

	err = o.controller.SetFilesystemReplication(o.config.CephFilesystem, factor)
	if err != nil {
		return errors.Wrapf(err, "set filesystem %s replication to %d", o.config.CephFilesystem, factor)
	}

	err = o.controller.SetObjectStoreReplication(o.config.CephObjectStore, factor)
	if err != nil {
		return errors.Wrapf(err, "set object store %s replication to %d", o.config.CephObjectStore, factor)
	}

	return nil
}
