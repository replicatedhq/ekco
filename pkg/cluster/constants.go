package cluster

import "k8s.io/apimachinery/pkg/labels"

const (
	RookCephNS                   = "rook-ceph"
	CephClusterName              = "rook-ceph"
	RookCephSharedFSMetadataPool = "rook-shared-fs-metadata"
	RookCephSharedFSDataPool     = "rook-shared-fs-data0"
	CephDeviceHealthMetricsPool  = "device_health_metrics"

	RookCephObjectStoreRootPool = ".rgw.root"

	PrimaryRoleLabel         = "node-role.kubernetes.io/master"
	RotateCertsLabel         = "kurl.sh/task"
	RotateCertsValue         = "rotate-certs"
	RotateCertsLastAttempted = "rotate-certs-last-attempted"

	TaskLabel             = "kurl.sh/task"
	UpdateInternalLBValue = "update-internallb"
)

var RotateCertsSelector = labels.SelectorFromSet(labels.Set{RotateCertsLabel: RotateCertsValue})

var UpdateInternalLBSelector = labels.SelectorFromSet(labels.Set{TaskLabel: UpdateInternalLBValue})

var (
	RookCephObjectStoreMetadataPools = []string{
		// .rgw.root (rootPool) is appended to this slice where needed
		"rgw.control",
		"rgw.meta",
		"rgw.log",
		"rgw.buckets.index",
		"rgw.buckets.non-ec",
	}
	RookCephObjectStoreDataPools = []string{
		"rgw.buckets.data",
	}
)
