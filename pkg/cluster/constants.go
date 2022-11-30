package cluster

import "k8s.io/apimachinery/pkg/labels"

const (
	RookCephNS                        = "rook-ceph"
	CephClusterName                   = "rook-ceph"
	RookCephSharedFSMetadataPool      = "rook-shared-fs-metadata"
	RookCephSharedFSDataPool          = "rook-shared-fs-data0"
	CephDeviceHealthMetricsPool       = "device_health_metrics"
	CephDeviceHealthMetricsPoolQuincy = ".mgr"

	RookCephObjectStoreRootPool = ".rgw.root"

	RotateCertsLabel         = "kurl.sh/task"
	RotateCertsValue         = "rotate-certs"
	RotateCertsLastAttempted = "rotate-certs-last-attempted"

	TaskLabel                = "kurl.sh/task"
	UpdateInternalLBValue    = "update-internallb"
	SetKubeconfigServerValue = "set-kubeconfig-server"
)

var RotateCertsSelector = labels.SelectorFromSet(labels.Set{RotateCertsLabel: RotateCertsValue})
var UpdateInternalLBSelector = labels.SelectorFromSet(labels.Set{TaskLabel: UpdateInternalLBValue})
var SetKubeconfigServerSelector = labels.SelectorFromSet(labels.Set{TaskLabel: SetKubeconfigServerValue})

var (
	RookCephObjectStoreMetadataPools = []string{
		// .rgw.root (rootPool) is appended to this slice where needed
		"rgw.control",
		"rgw.meta",
		"rgw.log",
		"rgw.buckets.index",
		"rgw.buckets.non-ec",
	}
	RookCephObjectStoreMetadataPoolsQuincy = []string{
		// .rgw.root (rootPool) is appended to this slice where needed
		"rgw.control",
		"rgw.meta",
		"rgw.log",
		"rgw.buckets.index",
		"rgw.buckets.non-ec",
		"rgw.otp",
	}
	RookCephObjectStoreDataPools = []string{
		"rgw.buckets.data",
	}
)
