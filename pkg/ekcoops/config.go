package ekcoops

import (
	"time"
)

type Config struct {
	// how long a Node must be unreachable before considered dead
	NodeUnreachableToleration time.Duration `mapstructure:"node_unreachable_toleration"`
	// don't purge if it will result in less than this many ready masters
	MinReadyMasterNodes int `mapstructure:"min_ready_master_nodes"`
	// don't purge if it will result in less than this many ready workers
	MinReadyWorkerNodes int `mapstructure:"min_ready_worker_nodes"`

	// whether to purge dead nodes from the cluster automatically
	PurgeDeadNodes bool `mapstructure:"purge_dead_nodes"`
	// whether to force delete terminating pods on dead nodes automatically
	ClearDeadNodes bool `mapstructure:"clear_dead_nodes"`

	// whether to maintain the list of nodes to use in the CephCluster config. With Rook 1.0
	// Replicated maintains the list to obviate the need to restart the operator, but with
	// earlier versions of Rook we just set `useAllNodes` to true. This also enables control of
	// the replication factor of ceph pools, scaling up and down with the number of nodes in the
	// cluster.
	MaintainRookStorageNodes bool `mapstructure:"maintain_rook_storage_nodes"`
	// when set, only nodes with this label will be added to the ceph cluster
	RookStorageNodesLabel string `mapstructure:"rook_storage_nodes_label"`
	// names and levels of ceph pools to maintain if MaintainRookStorageNodes is enabled
	CephBlockPool          string `mapstructure:"ceph_block_pool"`
	CephFilesystem         string `mapstructure:"ceph_filesystem"`
	CephObjectStore        string `mapstructure:"ceph_object_store"`
	MinCephPoolReplication int    `mapstructure:"min_ceph_pool_replication"`
	MaxCephPoolReplication int    `mapstructure:"max_ceph_pool_replication"`
	RookVersion            string `mapstructure:"rook_version"`

	// kubernetes certificates directory
	CertificatesDir string `mapstructure:"certificates_dir"`

	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"`
}
